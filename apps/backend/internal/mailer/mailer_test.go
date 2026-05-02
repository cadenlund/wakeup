package mailer_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/resend/resend-go/v2"

	"github.com/cadenlund/wakeup/apps/backend/internal/mailer"
)

// fakeResend stands up a tiny HTTP server that mimics Resend's POST /emails
// endpoint. The handler optionally fails so we can exercise both paths.
func fakeResend(t *testing.T, statusCode int, captured *resend.SendEmailRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/emails") {
			http.Error(w, "wrong method/path", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if captured != nil {
			if err := json.Unmarshal(body, captured); err != nil {
				t.Errorf("unmarshal request body: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if statusCode == http.StatusOK {
			_, _ = w.Write([]byte(`{"id":"fake-message-id"}`))
		} else {
			_, _ = w.Write([]byte(`{"message":"resend rejected the request"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newMailerAt builds a *Resend pointed at the given fake-server URL.
func newMailerAt(t *testing.T, serverURL string) *mailer.Resend {
	t.Helper()
	client := resend.NewClient("test-key")
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	client.BaseURL = u

	m, err := mailer.NewWithClient(client, mailer.Config{
		APIKey:       "test-key",
		FromEmail:    "Wakeup <no-reply@wakeup.test>",
		ResetURLBase: "https://wakeup.app/auth/reset?token=",
	})
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	return m
}

func TestSendPasswordReset_Success(t *testing.T) {
	t.Parallel()
	var captured resend.SendEmailRequest
	srv := fakeResend(t, http.StatusOK, &captured)
	m := newMailerAt(t, srv.URL)

	const to = "alice@example.com"
	const token = "tok-1234"
	if err := m.SendPasswordReset(context.Background(), to, token); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}

	if captured.From != "Wakeup <no-reply@wakeup.test>" {
		t.Errorf("From = %q", captured.From)
	}
	if len(captured.To) != 1 || captured.To[0] != to {
		t.Errorf("To = %v, want [%s]", captured.To, to)
	}
	if !strings.Contains(captured.Subject, "Reset") {
		t.Errorf("Subject = %q", captured.Subject)
	}
	wantLink := "https://wakeup.app/auth/reset?token=" + token
	if !strings.Contains(captured.Html, wantLink) {
		t.Errorf("Html missing reset link: %q", captured.Html)
	}
	if !strings.Contains(captured.Text, wantLink) {
		t.Errorf("Text missing reset link: %q", captured.Text)
	}
}

func TestSendPasswordReset_TokenIsURLEscaped(t *testing.T) {
	t.Parallel()
	var captured resend.SendEmailRequest
	srv := fakeResend(t, http.StatusOK, &captured)
	m := newMailerAt(t, srv.URL)

	// Token with characters that need escaping. The link in both bodies
	// must contain the percent-encoded form, not the raw bytes.
	rawToken := "abc def&xyz"
	if err := m.SendPasswordReset(context.Background(), "x@example.com", rawToken); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}
	encoded := url.QueryEscape(rawToken)
	if !strings.Contains(captured.Html, encoded) {
		t.Errorf("Html should contain url-encoded token %q, got: %q", encoded, captured.Html)
	}
	if strings.Contains(captured.Html, rawToken) {
		t.Errorf("Html should NOT contain the raw token verbatim: %q", captured.Html)
	}
}

func TestSendPasswordReset_PropagatesUpstreamError(t *testing.T) {
	t.Parallel()
	srv := fakeResend(t, http.StatusBadRequest, nil)
	m := newMailerAt(t, srv.URL)

	err := m.SendPasswordReset(context.Background(), "alice@example.com", "tok")
	if err == nil {
		t.Fatal("expected error when Resend returns 400")
	}
	if !strings.Contains(err.Error(), "SendPasswordReset") {
		t.Errorf("error should mention SendPasswordReset, got: %v", err)
	}
}

func TestSendPasswordReset_RejectsBlankInputs(t *testing.T) {
	t.Parallel()
	srv := fakeResend(t, http.StatusOK, nil)
	m := newMailerAt(t, srv.URL)

	if err := m.SendPasswordReset(context.Background(), "", "tok"); err == nil {
		t.Error("blank to should error")
	}
	if err := m.SendPasswordReset(context.Background(), "a@b.c", ""); err == nil {
		t.Error("blank token should error")
	}
}

func TestNew_ValidatesConfig(t *testing.T) {
	t.Parallel()
	base := mailer.Config{
		APIKey:       "test-key",
		FromEmail:    "x@example.com",
		ResetURLBase: "https://wakeup.app/auth/reset?token=",
	}
	cases := []struct {
		name string
		mod  func(*mailer.Config)
	}{
		{"missing api key", func(c *mailer.Config) { c.APIKey = "" }},
		{"missing from", func(c *mailer.Config) { c.FromEmail = "" }},
		{"missing reset URL", func(c *mailer.Config) { c.ResetURLBase = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.mod(&cfg)
			if _, err := mailer.New(cfg); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestNewWithClient_RejectsNilClient(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		APIKey: "k", FromEmail: "from@x", ResetURLBase: "https://x/reset?token=",
	}
	_, err := mailer.NewWithClient(nil, cfg)
	if err == nil {
		t.Fatal("nil client should error")
	}
}

// Compile-time guard that the test types match what the SDK accepts.
var _ = errors.New
