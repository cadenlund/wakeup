package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
)

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	t.Parallel()
	var seenID string
	h := mw.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenID = mw.RequestIDFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if seenID == "" {
		t.Fatal("ctx request id is empty")
	}
	if _, err := uuid.Parse(seenID); err != nil {
		t.Errorf("generated id should be a UUID, got %q: %v", seenID, err)
	}
	if got := rec.Header().Get(mw.RequestIDHeader); got != seenID {
		t.Errorf("response header %q != ctx %q", got, seenID)
	}
}

func TestRequestID_PreservesIncoming(t *testing.T) {
	t.Parallel()
	const incoming = "incoming-id-123"
	var seenID string
	h := mw.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenID = mw.RequestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(mw.RequestIDHeader, incoming)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seenID != incoming {
		t.Errorf("ctx id = %q, want %q", seenID, incoming)
	}
	if got := rec.Header().Get(mw.RequestIDHeader); got != incoming {
		t.Errorf("response header = %q, want %q", got, incoming)
	}
}
