package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
)

func TestSecurityHeaders_DefaultDevSet(t *testing.T) {
	t.Parallel()
	h := mw.SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS should be empty in dev, got %q", got)
	}
}

func TestSecurityHeaders_ProdSetsHSTS(t *testing.T) {
	t.Parallel()
	h := mw.SecurityHeaders(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Errorf("HSTS empty in prod")
	}
}
