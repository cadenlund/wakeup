package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
)

// fakeWriteError captures the *apierror.Error WriteError was called with
// and writes a §4.4 envelope, so the recovery test can assert the wire
// shape without importing the handler package.
func fakeWriteError(w http.ResponseWriter, _ *http.Request, err error) {
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		ae = apierror.Internal("internal error")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ae.HTTPStatus())
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code":    string(ae.Code),
		"message": ae.Message,
	}})
}

func TestRecovery_CatchesPanic(t *testing.T) {
	t.Parallel()
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := mw.Recovery(mw.RecoveryConfig{Logger: logger, WriteError: fakeWriteError})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "kaboom") {
		t.Fatalf("response leaked panic text: %s", body)
	}
	if !strings.Contains(body, "internal error") {
		t.Errorf("body missing generic message: %s", body)
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "panic recovered") {
		t.Errorf("log missing 'panic recovered': %s", logged)
	}
	if !strings.Contains(logged, "kaboom") {
		t.Errorf("log missing panic value: %s", logged)
	}
	if !strings.Contains(logged, "stack") {
		t.Errorf("log missing stack: %s", logged)
	}
}

func TestRecovery_ReraisesAbortHandler(t *testing.T) {
	t.Parallel()
	h := mw.Recovery(mw.RecoveryConfig{Logger: slog.Default(), WriteError: fakeWriteError})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected ErrAbortHandler to re-panic out of Recovery")
		}
		err, ok := r.(error)
		if !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("expected http.ErrAbortHandler, got %v", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestRecovery_NoPanicPassThrough(t *testing.T) {
	t.Parallel()
	h := mw.Recovery(mw.RecoveryConfig{WriteError: fakeWriteError})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", rec.Code)
	}
}

// fakeCapturer captures calls so the test can assert Recovery sent the
// panic + tags to the configured Capturer (in production, Sentry).
type fakeCapturer struct {
	captured []capturedEvent
}

type capturedEvent struct {
	err  error
	tags map[string]string
}

func (f *fakeCapturer) Capture(err error, tags map[string]string) {
	f.captured = append(f.captured, capturedEvent{err: err, tags: tags})
}

func TestRecovery_CapturesPanicToSentry(t *testing.T) {
	t.Parallel()
	capturer := &fakeCapturer{}
	// Wire RequestID upstream so the captured tags include the
	// `request_id` Recovery propagates — without it the assertion
	// below would only ever see "" and silently pass.
	h := mw.Recovery(mw.RecoveryConfig{
		Logger: slog.Default(), WriteError: fakeWriteError, Sentry: capturer,
	})(mw.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/foo", nil))

	if len(capturer.captured) != 1 {
		t.Fatalf("expected 1 capture, got %d", len(capturer.captured))
	}
	got := capturer.captured[0]
	if !strings.Contains(got.err.Error(), "boom") {
		t.Errorf("captured err missing panic value: %v", got.err)
	}
	if got.tags["method"] != "POST" || got.tags["path"] != "/v1/foo" {
		t.Errorf("captured tags missing route info: %+v", got.tags)
	}
	if got.tags["request_id"] == "" {
		t.Errorf("captured tags missing request_id: %+v", got.tags)
	}
}

func TestRecovery_NilSentryDoesNotCapture(t *testing.T) {
	t.Parallel()
	// Sentry is optional — when nil, Recovery should still log + return
	// the §4.4 envelope but skip capture.
	h := mw.Recovery(mw.RecoveryConfig{
		Logger: slog.Default(), WriteError: fakeWriteError, // Sentry: nil
	})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestRecovery_PanicsWhenNoWriteError(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected Recovery to panic on nil writeError")
		}
	}()
	_ = mw.Recovery(mw.RecoveryConfig{Logger: slog.Default()})
}
