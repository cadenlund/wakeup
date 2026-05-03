package sentry_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sentryclient "github.com/cadenlund/wakeup/apps/backend/internal/sentry"
)

// New rejects an empty DSN so missing-secret rollouts in non-dev envs
// fail fast at startup. cmd/server's buildSentry handles the dev case
// by returning nil before reaching New; this test pins the exact error
// message so a future refactor that swallows or rewords the validation
// failure surfaces here.
func TestNew_RejectsEmptyDSN(t *testing.T) {
	t.Parallel()
	const wantMsg = "sentry: Config.DSN is required"
	for _, dsn := range []string{"", "   ", "\t\n"} {
		_, err := sentryclient.New(sentryclient.Config{DSN: dsn})
		if err == nil {
			t.Errorf("New(%q): expected error, got nil", dsn)
			continue
		}
		if err.Error() != wantMsg {
			t.Errorf("New(%q): err = %q, want %q", dsn, err.Error(), wantMsg)
		}
	}
}

// A nil-receiver Capture is a no-op — buildSentry passes nil to the
// recovery middleware in dev, and the recovery code paths assume that
// a nil Capturer is safe to call without crashing.
func TestCapture_NilReceiverNoOp(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Capture on nil client should not panic, got %v", r)
		}
	}()
	var c *sentryclient.Client
	c.Capture(errors.New("boom"), map[string]string{"k": "v"})
}

// Flush on a nil client returns true (nothing to drain) — matches the
// SDK contract where a no-op flush is "successful".
func TestFlush_NilReceiverReturnsTrue(t *testing.T) {
	t.Parallel()
	var c *sentryclient.Client
	if !c.Flush(time.Second) {
		t.Error("nil-receiver Flush should return true")
	}
}

// End-to-end happy path: New with a real DSN pointed at a local
// recorder, Capture an event, Flush, and assert the upstream received
// the payload. Exercises the New success branch, Capture's
// hub.WithScope path, and Flush's hub.Flush path that the nil-receiver
// tests above can't reach.
//
// Not parallel: sentry.Init mutates package-global state in the
// upstream SDK, so concurrent New() calls in the same process step on
// each other.
func TestNew_CaptureFlushE2E(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sentry posts to /api/<project>/envelope/ — we accept
		// anything so a future SDK version that changes the path
		// still fires this handler.
		if r.Body != nil {
			_ = r.Body.Close()
		}
		select {
		case received <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse httptest URL: %v", err)
	}
	// DSN form: <scheme>://<key>@<host>/<projectid>
	dsn := u.Scheme + "://k@" + u.Host + "/1"

	c, err := sentryclient.New(sentryclient.Config{
		DSN:         dsn,
		Environment: "test",
		Release:     "test-release",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c == nil {
		t.Fatal("New returned nil client")
	}

	c.Capture(errors.New("boom"), map[string]string{"request_id": "abc"})

	if !c.Flush(5 * time.Second) {
		t.Error("Flush returned false (event still queued after timeout)")
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Error("upstream never received the captured event")
	}
}

// Capture with a nil tags map must still send (no scope tags) — the
// recovery middleware passes nil when no per-request context is
// available, and that path must not panic or short-circuit.
func TestCapture_NilTagsDoesNotPanic(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		select {
		case received <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	dsn := strings.Join([]string{u.Scheme, "://k@", u.Host, "/1"}, "")
	c, err := sentryclient.New(sentryclient.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Capture(errors.New("boom"), nil)
	c.Flush(5 * time.Second)
}
