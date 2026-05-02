package session_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/session"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// newTestServer builds an httptest.Server backed by the provided session
// manager. Two endpoints:
//
//	POST /login  — calls manager.Put("user_id", "alice") then 204
//	GET  /me     — returns the user_id from the session, or 401
//	POST /logout — calls manager.Destroy then 204
//
// All routes are wrapped in manager.LoadAndSave so the cookie is set on the
// response and read on subsequent requests.
func newTestServer(t *testing.T, m *http.ServeMux, sm sessionManager) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(sm.LoadAndSave(m))
	t.Cleanup(server.Close)
	return server
}

// sessionManager is the slice of *scs.SessionManager that newTestServer needs.
// Defining it as an interface lets the test live without importing scs by name.
type sessionManager interface {
	LoadAndSave(http.Handler) http.Handler
	Put(context.Context, string, any)
	GetString(context.Context, string) string
	Destroy(context.Context) error
	Cookie() http.Cookie
}

// scsAdapter exposes the methods sessionManager wants. *scs.SessionManager
// already has all of these except Cookie(); we wrap to satisfy the interface
// shape this test file uses.
type scsAdapter struct {
	mgr managerCookieReader
}

type managerCookieReader interface {
	LoadAndSave(http.Handler) http.Handler
	Put(context.Context, string, any)
	GetString(context.Context, string) string
	Destroy(context.Context) error
}

func (a *scsAdapter) LoadAndSave(h http.Handler) http.Handler { return a.mgr.LoadAndSave(h) }
func (a *scsAdapter) Put(ctx context.Context, k string, v any) {
	a.mgr.Put(ctx, k, v)
}
func (a *scsAdapter) GetString(ctx context.Context, k string) string {
	return a.mgr.GetString(ctx, k)
}
func (a *scsAdapter) Destroy(ctx context.Context) error {
	return a.mgr.Destroy(ctx)
}

// Cookie isn't used by any test; satisfies the interface above only because
// the test infrastructure happens to declare it. Returns the zero value.
func (a *scsAdapter) Cookie() http.Cookie { return http.Cookie{} }

func mustClient(t *testing.T, _ *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

// loginNoBody posts to /login and discards/closes the body so bodyclose
// is happy without sprinkling defer Close everywhere in the test bodies.
func loginNoBody(t *testing.T, client *http.Client, base string) {
	t.Helper()
	resp, err := client.Post(base+"/login", "", nil)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
}

// buildHandlerStack returns (server, client) for tests. The handler stack
// uses the project's session manager backed by the per-test pgtestdb.
func buildHandlerStack(t *testing.T) (*httptest.Server, *http.Client, sessionManager) {
	t.Helper()
	pool := testutil.NewTestDB(t)
	mgr := session.New(pool)
	adapter := &scsAdapter{mgr: mgr}

	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		mgr.Put(r.Context(), "user_id", "alice")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		uid := mgr.GetString(r.Context(), "user_id")
		if uid == "" {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(uid))
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		_ = mgr.Destroy(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	server := newTestServer(t, mux, adapter)
	client := mustClient(t, server)
	return server, client, adapter
}

func TestSession_CookieSetOnLogin(t *testing.T) {
	t.Parallel()
	server, client, _ := buildHandlerStack(t)

	resp, err := client.Post(server.URL+"/login", "", nil)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	// One Set-Cookie header for our session, with the right name + flags.
	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("expected wakeup_session cookie on /login response")
	}
	if !found.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if found.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", found.SameSite)
	}
	if found.Path != "/" {
		t.Errorf("cookie Path = %q, want /", found.Path)
	}

	// Session value round-trips on a subsequent request via the cookie jar.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/me", nil)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("/me status = %d, want 200", resp2.StatusCode)
	}
}

func TestSession_LogoutClearsCookie(t *testing.T) {
	t.Parallel()
	server, client, _ := buildHandlerStack(t)

	// Login first.
	loginNoBody(t, client, server.URL)
	// Logout.
	resp, err := client.Post(server.URL+"/logout", "", nil)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Subsequent /me must be 401 — even though the jar may still hold a
	// cookie, scs has marked it destroyed in the store + sent a Set-Cookie
	// with MaxAge<=0 to clear it.
	resp2, err := client.Get(server.URL + "/me")
	if err != nil {
		t.Fatalf("me after logout: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/me after logout = %d, want 401", resp2.StatusCode)
	}
}

func TestSession_TamperedCookieReturnsNoUser(t *testing.T) {
	t.Parallel()
	server, _, _ := buildHandlerStack(t)

	// Hand-craft a client whose jar has a fake session token.
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(server.URL)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  session.CookieName,
		Value: "TAMPERED_TOKEN_NOT_IN_DB",
		Path:  "/",
	}})
	client := &http.Client{Jar: jar}

	resp, err := client.Get(server.URL + "/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/me with tampered cookie = %d, want 401", resp.StatusCode)
	}
}

func TestSession_ExpiredSessionReturnsNoUser(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	mgr := session.New(pool)

	// Spin up a server, log in, then directly poke the sessions table to
	// expire the row. scs only honors rows with expiry > now() at LoadAndSave
	// time, so even with a valid cookie the user_id should be invisible.
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		mgr.Put(r.Context(), "user_id", "alice")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		if uid := mgr.GetString(r.Context(), "user_id"); uid == "" {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	server := httptest.NewServer(mgr.LoadAndSave(mux))
	t.Cleanup(server.Close)
	client := mustClient(t, server)

	// Login → cookie now in jar.
	loginNoBody(t, client, server.URL)

	// Force every session row to expire 1 hour ago.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		"UPDATE sessions SET expiry = now() - interval '1 hour'",
	); err != nil {
		t.Fatalf("expire UPDATE: %v", err)
	}

	resp, err := client.Get(server.URL + "/me")
	if err != nil {
		t.Fatalf("me after expire: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/me after expire = %d, want 401", resp.StatusCode)
	}
}

// CookieName is the wire identity. Locked.
func TestSession_CookieNameIsWakeup(t *testing.T) {
	t.Parallel()
	if !strings.EqualFold(session.CookieName, "wakeup_session") {
		t.Fatalf("CookieName = %q, want wakeup_session", session.CookieName)
	}
}
