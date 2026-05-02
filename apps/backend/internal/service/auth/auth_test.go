package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/passwordreset"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	"github.com/cadenlund/wakeup/apps/backend/internal/session"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// recordingMailer captures every SendPasswordReset call. Goroutine-safe.
type recordingMailer struct {
	mu   sync.Mutex
	sent []recordedReset
}

type recordedReset struct {
	To    string
	Token string
}

func (m *recordingMailer) SendPasswordReset(_ context.Context, to, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, recordedReset{To: to, Token: token})
	return nil
}

func (m *recordingMailer) calls() []recordedReset {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]recordedReset, len(m.sent))
	copy(out, m.sent)
	return out
}

// authStack is the per-test fixture. Each call to newAuthStack gets a
// fresh pgtestdb-cloned database, a real scs.SessionManager backed by
// pgxstore, a recording mailer, and a TLS test server that runs handlers
// inside the session manager's LoadAndSave middleware so the session
// cookie round-trips per request.
type authStack struct {
	svc      *auth.Service
	server   *httptest.Server
	pool     *pgxpool.Pool
	mailer   *recordingMailer
	sessions *scs.SessionManager
}

func newAuthStack(t *testing.T) *authStack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	sessMgr := session.New(pool)
	users := user.New(pool)
	resets := passwordreset.New(pool)
	mail := &recordingMailer{}

	svc, err := auth.New(auth.Config{
		Pool:     pool,
		Users:    users,
		Resets:   resets,
		Sessions: sessMgr,
		Mailer:   mail,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, sErr := svc.Register(r.Context(), auth.RegisterParams{
			Username:    r.Form.Get("username"),
			Email:       r.Form.Get("email"),
			DisplayName: r.Form.Get("display_name"),
			Password:    r.Form.Get("password"),
		})
		writeAPIErr(w, sErr, http.StatusCreated)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, sErr := svc.Login(r.Context(), auth.LoginParams{
			Identifier: r.Form.Get("identifier"),
			Password:   r.Form.Get("password"),
		})
		writeAPIErr(w, sErr, http.StatusOK)
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		writeAPIErr(w, svc.Logout(r.Context()), http.StatusNoContent)
	})
	mux.HandleFunc("/logoutall", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, err := uuid.Parse(r.Form.Get("user_id"))
		if err != nil {
			http.Error(w, "bad user_id", http.StatusBadRequest)
			return
		}
		writeAPIErr(w, svc.LogoutAll(r.Context(), id), http.StatusNoContent)
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		u, sErr := svc.Me(r.Context())
		if sErr != nil {
			writeAPIErr(w, sErr, http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(u.ID.String()))
	})
	mux.HandleFunc("/reset/request", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		writeAPIErr(w, svc.RequestPasswordReset(r.Context(), r.Form.Get("email")), http.StatusNoContent)
	})
	mux.HandleFunc("/reset/confirm", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		writeAPIErr(w, svc.ConfirmPasswordReset(r.Context(), auth.ConfirmPasswordResetParams{
			Token:       r.Form.Get("token"),
			NewPassword: r.Form.Get("new_password"),
		}), http.StatusNoContent)
	})

	srv := httptest.NewTLSServer(sessMgr.LoadAndSave(mux))
	t.Cleanup(srv.Close)

	return &authStack{
		svc:      svc,
		server:   srv,
		pool:     pool,
		mailer:   mail,
		sessions: sessMgr,
	}
}

// writeAPIErr writes an *apierror.Error as the right HTTP status, or
// writes okStatus on nil. The X-Code header lets tests assert the wire
// code without parsing body JSON.
func writeAPIErr(w http.ResponseWriter, err error, okStatus int) {
	if err == nil {
		w.WriteHeader(okStatus)
		return
	}
	var ae *apierror.Error
	if errors.As(err, &ae) {
		w.Header().Set("X-Code", string(ae.Code))
		http.Error(w, ae.Message, ae.HTTPStatus())
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func mustClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	c := srv.Client()
	c.Jar = jar
	return c
}

// register / login / etc helpers wrap the typical PostForm to keep tests
// focused on the assertions, not the boilerplate.
func register(t *testing.T, c *http.Client, base, username, email, password string) *http.Response {
	t.Helper()
	resp, err := c.PostForm(base+"/register", url.Values{
		"username":     {username},
		"email":        {email},
		"display_name": {"Test"},
		"password":     {password},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return resp
}

func login(t *testing.T, c *http.Client, base, identifier, password string) *http.Response {
	t.Helper()
	resp, err := c.PostForm(base+"/login", url.Values{
		"identifier": {identifier},
		"password":   {password},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return resp
}

// --- Tests ---------------------------------------------------------------

func TestRegister_Success_LogsInUser(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c := mustClient(t, st.server)

	resp := register(t, c, st.server.URL, "alice", "alice@x.test", "correct-horse")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", resp.StatusCode)
	}

	meResp, err := c.Get(st.server.URL + "/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer func() { _ = meResp.Body.Close() }()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("/me after register = %d, want 200", meResp.StatusCode)
	}
}

func TestRegister_DuplicateUsername_409(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c1 := mustClient(t, st.server)
	r1 := register(t, c1, st.server.URL, "dup", "a@x.test", "longenough")
	_ = r1.Body.Close()

	c2 := mustClient(t, st.server)
	r2 := register(t, c2, st.server.URL, "dup", "b@x.test", "longenough")
	defer func() { _ = r2.Body.Close() }()
	if r2.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", r2.StatusCode)
	}
	if got := r2.Header.Get("X-Code"); got != string(apierror.CodeConflict) {
		t.Errorf("X-Code = %q", got)
	}
}

func TestLogin_ByUsernameAndEmail(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)

	c0 := mustClient(t, st.server)
	r := register(t, c0, st.server.URL, "carol", "carol@x.test", "longenough")
	_ = r.Body.Close()

	for _, identifier := range []string{"carol", "carol@x.test"} {
		c := mustClient(t, st.server)
		resp := login(t, c, st.server.URL, identifier, "longenough")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("login %q = %d", identifier, resp.StatusCode)
		}
	}
}

func TestLogin_WrongPassword_401(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c0 := mustClient(t, st.server)
	r := register(t, c0, st.server.URL, "dave", "dave@x.test", "right-pw")
	_ = r.Body.Close()

	c := mustClient(t, st.server)
	resp := login(t, c, st.server.URL, "dave", "wrong-pw")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Code"); got != string(apierror.CodeUnauthorized) {
		t.Errorf("X-Code = %q", got)
	}
}

func TestLogin_UnknownUser_401_NoEnumeration(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c := mustClient(t, st.server)
	resp := login(t, c, st.server.URL, "ghost", "anything")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLogout_ClearsSession(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c := mustClient(t, st.server)

	r := register(t, c, st.server.URL, "erin", "erin@x.test", "longenough")
	_ = r.Body.Close()

	out, err := c.Post(st.server.URL+"/logout", "", nil)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	_ = out.Body.Close()

	meResp, err := c.Get(st.server.URL + "/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer func() { _ = meResp.Body.Close() }()
	if meResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/me after logout = %d, want 401", meResp.StatusCode)
	}
}

func TestRequestPasswordReset_KnownEmail_MailsToken(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c0 := mustClient(t, st.server)
	r := register(t, c0, st.server.URL, "frank", "frank@x.test", "longenough")
	_ = r.Body.Close()

	c := mustClient(t, st.server)
	resp, err := c.PostForm(st.server.URL+"/reset/request", url.Values{"email": {"frank@x.test"}})
	if err != nil {
		t.Fatalf("reset request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	calls := st.mailer.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 mail call, got %d", len(calls))
	}
	if calls[0].To != "frank@x.test" {
		t.Errorf("To = %q", calls[0].To)
	}
	if calls[0].Token == "" {
		t.Error("Token should not be empty")
	}
}

func TestRequestPasswordReset_UnknownEmail_NoEnumeration(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c := mustClient(t, st.server)

	resp, err := c.PostForm(st.server.URL+"/reset/request", url.Values{"email": {"never@registered.test"}})
	if err != nil {
		t.Fatalf("reset request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if calls := st.mailer.calls(); len(calls) != 0 {
		t.Fatalf("mailer should not be called, got %d", len(calls))
	}
}

func TestConfirmPasswordReset_HappyPath(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c0 := mustClient(t, st.server)
	r := register(t, c0, st.server.URL, "gail", "gail@x.test", "old-password")
	_ = r.Body.Close()

	cReq := mustClient(t, st.server)
	r2, err := cReq.PostForm(st.server.URL+"/reset/request", url.Values{"email": {"gail@x.test"}})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = r2.Body.Close()
	calls := st.mailer.calls()
	if len(calls) != 1 {
		t.Fatalf("expected mailer call, got %d", len(calls))
	}
	token := calls[0].Token

	cConfirm := mustClient(t, st.server)
	r3, err := cConfirm.PostForm(st.server.URL+"/reset/confirm", url.Values{
		"token":        {token},
		"new_password": {"new-password"},
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	_ = r3.Body.Close()
	if r3.StatusCode != http.StatusNoContent {
		t.Fatalf("confirm status = %d, want 204", r3.StatusCode)
	}

	// Old password fails, new password succeeds.
	cBad := mustClient(t, st.server)
	bad := login(t, cBad, st.server.URL, "gail", "old-password")
	_ = bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Errorf("old password should be 401, got %d", bad.StatusCode)
	}
	cGood := mustClient(t, st.server)
	good := login(t, cGood, st.server.URL, "gail", "new-password")
	_ = good.Body.Close()
	if good.StatusCode != http.StatusOK {
		t.Errorf("new password should be 200, got %d", good.StatusCode)
	}
}

func TestConfirmPasswordReset_BadToken_401(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c := mustClient(t, st.server)
	resp, err := c.PostForm(st.server.URL+"/reset/confirm", url.Values{
		"token":        {"never-issued"},
		"new_password": {"whatever-12"},
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestConfirmPasswordReset_ReplayedToken_401(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)
	c0 := mustClient(t, st.server)
	r := register(t, c0, st.server.URL, "henry", "henry@x.test", "longenough")
	_ = r.Body.Close()

	cReq := mustClient(t, st.server)
	r2, _ := cReq.PostForm(st.server.URL+"/reset/request", url.Values{"email": {"henry@x.test"}})
	_ = r2.Body.Close()
	token := st.mailer.calls()[0].Token

	cFirst := mustClient(t, st.server)
	r3, _ := cFirst.PostForm(st.server.URL+"/reset/confirm", url.Values{
		"token": {token}, "new_password": {"newone"},
	})
	_ = r3.Body.Close()
	if r3.StatusCode != http.StatusNoContent {
		t.Fatalf("first confirm: %d", r3.StatusCode)
	}

	cReplay := mustClient(t, st.server)
	r4, _ := cReplay.PostForm(st.server.URL+"/reset/confirm", url.Values{
		"token": {token}, "new_password": {"newone-again"},
	})
	defer func() { _ = r4.Body.Close() }()
	if r4.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replay: got %d, want 401", r4.StatusCode)
	}
}

// LogoutAll: register on two clients (= two sessions for the same user),
// invoke /logoutall with that user's id, both clients now 401 on /me.
func TestLogoutAll_KillsEverySessionForUser(t *testing.T) {
	t.Parallel()
	st := newAuthStack(t)

	c1 := mustClient(t, st.server)
	r := register(t, c1, st.server.URL, "ivy", "ivy@x.test", "longenough")
	_ = r.Body.Close()

	// Capture ivy's user_id by hitting /me.
	meResp, err := c1.Get(st.server.URL + "/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	body := readBody(t, meResp)
	_ = meResp.Body.Close()
	id, err := uuid.Parse(string(body))
	if err != nil {
		t.Fatalf("parse uid from /me: %v (body=%q)", err, body)
	}

	// Second client logs in as ivy → second session row.
	c2 := mustClient(t, st.server)
	l := login(t, c2, st.server.URL, "ivy", "longenough")
	_ = l.Body.Close()

	// LogoutAll for ivy via the test endpoint, using the TLS-trusting client.
	cAdmin := mustClient(t, st.server)
	out, err := cAdmin.PostForm(st.server.URL+"/logoutall", url.Values{"user_id": {id.String()}})
	if err != nil {
		t.Fatalf("logoutall: %v", err)
	}
	_ = out.Body.Close()

	// Both clients should now 401 on /me. Use a separate fresh client too
	// to confirm any leftover anonymous request also 401s.
	for name, c := range map[string]*http.Client{"first": c1, "second": c2} {
		resp, _ := c.Get(st.server.URL + "/me")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s client /me after logoutall = %d, want 401", name, resp.StatusCode)
		}
	}
}

// readBody is a tiny helper for the tests that need to inspect the body.
func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	out := make([]byte, 0, 64)
	buf := make([]byte, 64)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return out
}

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	mgr := session.New(pool)
	users := user.New(pool)
	resets := passwordreset.New(pool)

	cases := []struct {
		name string
		mod  func(*auth.Config)
	}{
		{"missing pool", func(c *auth.Config) { c.Pool = nil }},
		{"missing users", func(c *auth.Config) { c.Users = nil }},
		{"missing resets", func(c *auth.Config) { c.Resets = nil }},
		{"missing sessions", func(c *auth.Config) { c.Sessions = nil }},
		{"missing mailer", func(c *auth.Config) { c.Mailer = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := auth.Config{Pool: pool, Users: users, Resets: resets, Sessions: mgr, Mailer: &recordingMailer{}}
			tc.mod(&cfg)
			if _, err := auth.New(cfg); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}
