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
	h := mw.Recovery(logger, fakeWriteError)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
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
	h := mw.Recovery(slog.Default(), fakeWriteError)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
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
	h := mw.Recovery(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", rec.Code)
	}
}

func TestRecovery_FallbackPlaintextWhenNoWriteError(t *testing.T) {
	t.Parallel()
	h := mw.Recovery(slog.Default(), nil)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal error") {
		t.Errorf("plain body missing: %q", rec.Body.String())
	}
}
