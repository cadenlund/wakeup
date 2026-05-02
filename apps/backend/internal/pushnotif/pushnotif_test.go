package pushnotif_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/pushnotif"
)

// captured holds the parsed JSON body the fake server received.
type captured struct {
	To    []string       `json:"to"`
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Data  map[string]any `json:"data,omitempty"`
}

// fakeExpo stands up an httptest.Server that mimics Expo's push endpoint.
// statusCode is the HTTP status it returns; body is the JSON it returns.
// If capture is non-nil, the request body is unmarshaled into it.
// If capturedAuth is non-nil, the Authorization header is captured.
func fakeExpo(t *testing.T, statusCode int, body string, capture *captured, capturedAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "wrong method", http.StatusBadRequest)
			return
		}
		if capturedAuth != nil {
			*capturedAuth = r.Header.Get("Authorization")
		}
		raw, _ := io.ReadAll(r.Body)
		if capture != nil {
			if err := json.Unmarshal(raw, capture); err != nil {
				t.Errorf("unmarshal request: %v (raw: %s)", err, raw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newPusher(t *testing.T, endpoint string) *pushnotif.ExpoPusher {
	t.Helper()
	p, err := pushnotif.NewWithHTTP(
		pushnotif.Config{AccessToken: "test-token-abc"},
		http.DefaultClient,
		endpoint,
	)
	if err != nil {
		t.Fatalf("NewWithHTTP: %v", err)
	}
	return p
}

func TestSend_PostsJSONWithBearerAuth(t *testing.T) {
	t.Parallel()
	var got captured
	var auth string
	srv := fakeExpo(t, http.StatusOK,
		`{"data":[{"status":"ok","id":"r-1"},{"status":"ok","id":"r-2"}]}`,
		&got, &auth,
	)
	p := newPusher(t, srv.URL)

	tokens := []string{"ExponentPushToken[xxx1]", "ExponentPushToken[xxx2]"}
	err := p.Send(context.Background(), tokens, pushnotif.Notification{
		Title: "New message",
		Body:  "from caden",
		Data:  map[string]any{"type": "message", "conversation_id": "conv-1"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if auth != "Bearer test-token-abc" {
		t.Errorf("Authorization header = %q, want Bearer test-token-abc", auth)
	}
	if len(got.To) != 2 || got.To[0] != tokens[0] || got.To[1] != tokens[1] {
		t.Errorf("To = %v, want %v", got.To, tokens)
	}
	if got.Title != "New message" || got.Body != "from caden" {
		t.Errorf("Title/Body wrong: %+v", got)
	}
	if got.Data["type"] != "message" {
		t.Errorf("Data.type = %v, want message", got.Data["type"])
	}
}

func TestSend_EmptyTokensIsNoOp(t *testing.T) {
	t.Parallel()
	var serverHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverHits++
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	p := newPusher(t, srv.URL)
	err := p.Send(context.Background(), nil, pushnotif.Notification{Title: "T", Body: "B"})
	if err != nil {
		t.Fatalf("empty tokens should be no-op, got: %v", err)
	}
	err = p.Send(context.Background(), []string{}, pushnotif.Notification{Title: "T", Body: "B"})
	if err != nil {
		t.Fatalf("empty tokens should be no-op, got: %v", err)
	}
	if serverHits != 0 {
		t.Fatalf("server should not be called, got %d hits", serverHits)
	}
}

func TestSend_RejectsBlankTitleOrBody(t *testing.T) {
	t.Parallel()
	srv := fakeExpo(t, http.StatusOK, `{"data":[{"status":"ok"}]}`, nil, nil)
	p := newPusher(t, srv.URL)
	tok := []string{"ExponentPushToken[t]"}

	if err := p.Send(context.Background(), tok, pushnotif.Notification{Title: "", Body: "B"}); err == nil {
		t.Error("blank Title should error")
	}
	if err := p.Send(context.Background(), tok, pushnotif.Notification{Title: "T", Body: ""}); err == nil {
		t.Error("blank Body should error")
	}
}

func TestSend_NonOKStatus_Errors(t *testing.T) {
	t.Parallel()
	srv := fakeExpo(t, http.StatusUnauthorized, `{"errors":[{"code":"PUSH_TOO_MANY","message":"unauthorized"}]}`, nil, nil)
	p := newPusher(t, srv.URL)

	err := p.Send(context.Background(), []string{"x"}, pushnotif.Notification{Title: "T", Body: "B"})
	if err == nil {
		t.Fatal("non-OK status should error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}

func TestSend_TopLevelError_Errors(t *testing.T) {
	t.Parallel()
	srv := fakeExpo(t, http.StatusOK,
		`{"data":[],"errors":[{"code":"VALIDATION_ERROR","message":"bad payload"}]}`, nil, nil,
	)
	p := newPusher(t, srv.URL)

	err := p.Send(context.Background(), []string{"x"}, pushnotif.Notification{Title: "T", Body: "B"})
	if err == nil {
		t.Fatal("top-level errors[] should propagate")
	}
	if !strings.Contains(err.Error(), "VALIDATION_ERROR") {
		t.Errorf("error should include code: %v", err)
	}
}

func TestSend_PerTicketError_Errors(t *testing.T) {
	t.Parallel()
	srv := fakeExpo(t, http.StatusOK,
		`{"data":[{"status":"ok","id":"r-1"},{"status":"error","message":"DeviceNotRegistered"}]}`, nil, nil,
	)
	p := newPusher(t, srv.URL)

	err := p.Send(context.Background(), []string{"a", "b"}, pushnotif.Notification{Title: "T", Body: "B"})
	if err == nil {
		t.Fatal("any ticket with status!=ok must surface an error")
	}
	if !strings.Contains(err.Error(), "DeviceNotRegistered") {
		t.Errorf("error should include ticket message: %v", err)
	}
}

func TestSend_DataMutationDoesNotAffectSentRequest(t *testing.T) {
	t.Parallel()
	var got captured
	srv := fakeExpo(t, http.StatusOK, `{"data":[{"status":"ok"}]}`, &got, nil)
	p := newPusher(t, srv.URL)

	data := map[string]any{"type": "msg"}
	if err := p.Send(context.Background(), []string{"x"}, pushnotif.Notification{
		Title: "T", Body: "B", Data: data,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Mutate after Send returns.
	data["type"] = "different"

	if got.Data["type"] != "msg" {
		t.Fatalf("recorded Data was mutated by caller-side change: %v", got.Data)
	}
}

func TestNew_RequiresAccessToken(t *testing.T) {
	t.Parallel()
	if _, err := pushnotif.New(pushnotif.Config{}); err == nil {
		t.Fatal("blank token should error")
	}
}

func TestNewWithHTTP_RejectsBadInputs(t *testing.T) {
	t.Parallel()
	cfg := pushnotif.Config{AccessToken: "k"}
	if _, err := pushnotif.NewWithHTTP(cfg, nil, "http://x"); err == nil {
		t.Error("nil client should error")
	}
	if _, err := pushnotif.NewWithHTTP(cfg, http.DefaultClient, ""); err == nil {
		t.Error("blank endpoint should error")
	}
	if _, err := pushnotif.NewWithHTTP(pushnotif.Config{}, http.DefaultClient, "http://x"); err == nil {
		t.Error("blank token should error")
	}
}
