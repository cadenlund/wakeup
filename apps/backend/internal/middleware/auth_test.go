package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
	"github.com/cadenlund/wakeup/apps/backend/internal/session"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

func parseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

// loadUserStack wires LoadUser + a downstream handler that records the
// resolved user, behind scs.LoadAndSave so session reads work. Uses the
// harness user service (the production UserLoader) so the middleware
// goes through the §4.1 service boundary.
func loadUserStack(t *testing.T, h *testutil.Harness, downstream http.HandlerFunc) http.Handler {
	t.Helper()
	chain := mw.LoadUser(h.Sessions, h.UserSvc, fakeWriteError)(downstream)
	return h.Sessions.LoadAndSave(chain)
}

func TestLoadUser_NoSession_LeavesCtxUnset(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	var got *domain.User
	stack := loadUserStack(t, h, func(_ http.ResponseWriter, r *http.Request) {
		got = mw.UserFromContext(r.Context())
	})
	stack.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if got != nil {
		t.Errorf("expected nil user, got %+v", got)
	}
}

func TestLoadUser_ValidSession_PopulatesCtx(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)

	// Drive a real register so scs.SessionManager has a session that maps
	// to a real user_id. We then forge a request whose session token
	// matches the one in the cookie jar.
	c, u := h.AuthClient(t)
	srvURL := c.Jar
	_ = srvURL // jar already holds the cookie
	cookies := c.Jar.Cookies(parseURL(t, h.Server.URL))
	if len(cookies) == 0 {
		t.Fatal("AuthClient should have set a session cookie")
	}

	// Build a request whose Cookie header carries the live session.
	stack := loadUserStack(t, h, func(_ http.ResponseWriter, r *http.Request) {
		got := mw.UserFromContext(r.Context())
		if got == nil {
			t.Errorf("expected user in ctx")
			return
		}
		if got.ID != u.ID {
			t.Errorf("user.ID = %s, want %s", got.ID, u.ID)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, ck := range cookies {
		if ck.Name == session.CookieName {
			req.AddCookie(ck)
		}
	}
	stack.ServeHTTP(httptest.NewRecorder(), req)
}

func TestLoadUser_GarbledUserID_ClearsSession(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	stack := h.Sessions.LoadAndSave(mw.LoadUser(h.Sessions, h.UserSvc, fakeWriteError)(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			if got := mw.UserFromContext(r.Context()); got != nil {
				t.Errorf("expected nil user, got %+v", got)
			}
		},
	)))

	// Inject a garbage user_id onto a fresh session via a setup handler
	// that runs scs.LoadAndSave first.
	setup := h.Sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		h.Sessions.Put(r.Context(), mw.SessionUserIDKey, "not-a-uuid")
	}))
	rec1 := httptest.NewRecorder()
	setup.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := rec1.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	stack.ServeHTTP(httptest.NewRecorder(), req)
}

func TestRequireAuth_NoUser_401(t *testing.T) {
	t.Parallel()
	stack := mw.RequireAuth(fakeWriteError)(http.HandlerFunc(
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("downstream handler should not have been called")
		},
	))
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireAuth_HasUser_PassesThrough(t *testing.T) {
	t.Parallel()
	called := false
	stack := mw.RequireAuth(fakeWriteError)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		},
	))
	u := &domain.User{ID: uuid.New(), Role: "user"}
	req := httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(mw.WithUser(context.Background(), u))
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, req)
	if !called {
		t.Error("downstream not called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRequireAdmin_NonAdmin_403(t *testing.T) {
	t.Parallel()
	stack := mw.RequireAdmin(fakeWriteError)(http.HandlerFunc(
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("downstream handler should not have been called")
		},
	))
	u := &domain.User{ID: uuid.New(), Role: "user"}
	req := httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(mw.WithUser(context.Background(), u))
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestRequireAdmin_Admin_PassesThrough(t *testing.T) {
	t.Parallel()
	called := false
	stack := mw.RequireAdmin(fakeWriteError)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		},
	))
	u := &domain.User{ID: uuid.New(), Role: mw.RoleAdmin}
	req := httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(mw.WithUser(context.Background(), u))
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, req)
	if !called {
		t.Error("downstream not called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRequireAdmin_NoUser_401(t *testing.T) {
	t.Parallel()
	stack := mw.RequireAdmin(fakeWriteError)(http.HandlerFunc(
		func(_ http.ResponseWriter, _ *http.Request) {
			t.Error("downstream handler should not have been called")
		},
	))
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// silence apierror import if it ever drops to 0 references in tests.
var _ = apierror.CodeUnauthorized
