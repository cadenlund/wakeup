package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
)

func TestBlockDuringImpersonation_PassesWhenNotImpersonating(t *testing.T) {
	t.Parallel()
	called := false
	stack := mw.BlockDuringImpersonation(fakeWriteError)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		},
	))
	u := &domain.User{ID: uuid.New(), Role: "user"}
	ctx := mw.WithUser(context.Background(), u)
	ctx = mw.WithRealUser(ctx, u)
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx))
	if !called {
		t.Error("downstream handler should have been invoked")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestBlockDuringImpersonation_BlocksWhenIDsDiffer(t *testing.T) {
	t.Parallel()
	stack := mw.BlockDuringImpersonation(fakeWriteError)(http.HandlerFunc(
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("downstream should NOT be called during impersonation")
		},
	))
	target := &domain.User{ID: uuid.New(), Role: "user"}
	admin := &domain.User{ID: uuid.New(), Role: "admin"}
	ctx := mw.WithUser(context.Background(), target)
	ctx = mw.WithRealUser(ctx, admin)
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/", nil).WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, string(apierror.CodeBlockedDuringImpersonation)) {
		t.Errorf("body should contain BLOCKED_DURING_IMPERSONATION code: %s", body)
	}
}

// Anonymous request: no ctx.User / ctx.RealUser. The guard must NOT
// fire on this — RequireAuth (upstream in real chains) should produce
// the 401 instead. Without that ordering, an unauthenticated POST would
// surface as 403 BLOCKED rather than 401, which is wrong.
func TestBlockDuringImpersonation_NoUserPassesThrough(t *testing.T) {
	t.Parallel()
	called := false
	stack := mw.BlockDuringImpersonation(fakeWriteError)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		},
	))
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if !called {
		t.Error("downstream should run when no user is loaded")
	}
}

// contains is a tiny helper local to this file (the auth_test.go has
// its own subset, but staying self-contained here avoids cross-test
// coupling).
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}())
}
