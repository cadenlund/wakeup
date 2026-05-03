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
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil/fixtures"
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

// --- §8.7 impersonation overlay ----------------------------------------

// seedSession attaches `keys` (key=value pairs) to a fresh scs session
// and returns the session cookie. Lets impersonation tests forge a
// session with both user_id and impersonating_user_id without going
// through the admin handler (which lands in milestone 12.5).
func seedSession(t *testing.T, h *testutil.Harness, keys map[string]string) *http.Cookie {
	t.Helper()
	setup := h.Sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		for k, v := range keys {
			h.Sessions.Put(r.Context(), k, v)
		}
	}))
	rec := httptest.NewRecorder()
	setup.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("seedSession: no cookie issued")
	}
	return cookies[0]
}

func TestLoadUser_Impersonation_OverridesEffectiveUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := testutil.New(t)

	// Admin owns the session; target is the impersonated user. Pass
	// explicit usernames since fixtures.MakeUser derives the default
	// from the first 8 chars of a UUIDv7, which can collide when two
	// users are inserted back-to-back in the same millisecond.
	admin := fixtures.MakeUser(t, h.DB,
		fixtures.WithRole("admin"),
		fixtures.WithUsername("imp-admin-"+uuid.Must(uuid.NewV7()).String()),
	)
	target := fixtures.MakeUser(t, h.DB,
		fixtures.WithUsername("imp-target-"+uuid.Must(uuid.NewV7()).String()),
	)

	cookie := seedSession(t, h, map[string]string{
		mw.SessionUserIDKey:        admin.ID.String(),
		mw.SessionImpersonatingKey: target.ID.String(),
	})

	stack := loadUserStack(t, h, func(_ http.ResponseWriter, r *http.Request) {
		eff := mw.UserFromContext(r.Context())
		realUser := mw.RealUserFromContext(r.Context())
		if eff == nil || eff.ID != target.ID {
			t.Errorf("ctx.User = %+v, want target %v", eff, target.ID)
		}
		if realUser == nil || realUser.ID != admin.ID {
			t.Errorf("ctx.RealUser = %+v, want admin %v", realUser, admin.ID)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	stack.ServeHTTP(httptest.NewRecorder(), req)
	_ = ctx
}

func TestLoadUser_Impersonation_AbsentBothEqualSessionOwner(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)

	admin := fixtures.MakeUser(t, h.DB,
		fixtures.WithRole("admin"),
		fixtures.WithUsername("imp-absent-"+uuid.Must(uuid.NewV7()).String()),
	)
	cookie := seedSession(t, h, map[string]string{
		mw.SessionUserIDKey: admin.ID.String(),
	})

	stack := loadUserStack(t, h, func(_ http.ResponseWriter, r *http.Request) {
		eff := mw.UserFromContext(r.Context())
		realUser := mw.RealUserFromContext(r.Context())
		if eff == nil || realUser == nil {
			t.Fatalf("expected both ctx users populated, got eff=%v real=%v", eff, realUser)
		}
		if eff.ID != realUser.ID {
			t.Errorf("without impersonation, ctx.User must equal ctx.RealUser; got %v vs %v", eff.ID, realUser.ID)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	stack.ServeHTTP(httptest.NewRecorder(), req)
}

func TestLoadUser_Impersonation_GarbledTargetClearsField(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)

	admin := fixtures.MakeUser(t, h.DB,
		fixtures.WithRole("admin"),
		fixtures.WithUsername("imp-garbled-"+uuid.Must(uuid.NewV7()).String()),
	)
	cookie := seedSession(t, h, map[string]string{
		mw.SessionUserIDKey:        admin.ID.String(),
		mw.SessionImpersonatingKey: "not-a-uuid",
	})

	stack := loadUserStack(t, h, func(_ http.ResponseWriter, r *http.Request) {
		eff := mw.UserFromContext(r.Context())
		realUser := mw.RealUserFromContext(r.Context())
		// Garbled field is dropped silently → admin keeps acting as
		// themselves (no 500, no impersonation override).
		if eff == nil || eff.ID != admin.ID {
			t.Errorf("ctx.User = %+v, want admin %v after dropping garbled field", eff, admin.ID)
		}
		if realUser == nil || realUser.ID != admin.ID {
			t.Errorf("ctx.RealUser = %+v, want admin %v", realUser, admin.ID)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	stack.ServeHTTP(httptest.NewRecorder(), req)
}

func TestLoadUser_Impersonation_SoftDeletedTargetClearsField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := testutil.New(t)

	admin := fixtures.MakeUser(t, h.DB,
		fixtures.WithRole("admin"),
		fixtures.WithUsername("imp-deleted-admin-"+uuid.Must(uuid.NewV7()).String()),
	)
	target := fixtures.MakeUser(t, h.DB,
		fixtures.WithUsername("imp-deleted-target-"+uuid.Must(uuid.NewV7()).String()),
	)
	if err := h.UserRepo.SoftDelete(ctx, target.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	cookie := seedSession(t, h, map[string]string{
		mw.SessionUserIDKey:        admin.ID.String(),
		mw.SessionImpersonatingKey: target.ID.String(),
	})

	stack := loadUserStack(t, h, func(_ http.ResponseWriter, r *http.Request) {
		eff := mw.UserFromContext(r.Context())
		// Target was deleted out from under the session → middleware
		// clears the field and admin acts as themselves.
		if eff == nil || eff.ID != admin.ID {
			t.Errorf("ctx.User = %+v, want admin %v after dropping deleted target", eff, admin.ID)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	stack.ServeHTTP(httptest.NewRecorder(), req)
}

// silence apierror import if it ever drops to 0 references in tests.
var _ = apierror.CodeUnauthorized
