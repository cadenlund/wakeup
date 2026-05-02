package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// errEnvelope mirrors the §4.4 wire shape for error responses so tests can
// decode and assert on Code, Fields, etc. without re-rolling the struct in
// every subtest.
type errEnvelope struct {
	Error apierror.Error `json:"error"`
}

func decodeErr(t *testing.T, body io.Reader) errEnvelope {
	t.Helper()
	var env errEnvelope
	if err := json.NewDecoder(body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return env
}

// post is a small helper: client.Post with an arbitrary body shape. Body
// can be a string (raw), []byte, or any JSON-marshalable value.
func post(t *testing.T, c *http.Client, urlStr string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
		// nil → empty body
	case string:
		rdr = strings.NewReader(b)
	case []byte:
		rdr = bytes.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	resp, err := c.Post(urlStr, "application/json", rdr)
	if err != nil {
		t.Fatalf("post %s: %v", urlStr, err)
	}
	return resp
}

func assertCode(t *testing.T, resp *http.Response, wantStatus int, wantCode apierror.Code) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, wantStatus, body)
	}
	if wantCode == "" {
		return
	}
	env := decodeErr(t, resp.Body)
	if env.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q", env.Error.Code, wantCode)
	}
}

func assertRequestIDEcho(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.Header.Get("X-Request-ID") == "" {
		t.Errorf("X-Request-ID response header is empty")
	}
}

// validRegisterBody returns a fresh, schema-valid register payload. Each
// call yields a unique username/email so subtests in the same harness
// don't collide. Username is padded so the smallest counter value still
// clears the §4.6 3-char minimum.
func validRegisterBody(t *testing.T) map[string]any {
	t.Helper()
	// 8-char zero-padded base36 counter → "0000000a" through "zzzzzzzz".
	// Base36 keeps it alphanumeric (validator rejects underscores etc.).
	suffix := testutil.NextSuffix()
	if len(suffix) < 8 {
		suffix = strings.Repeat("0", 8-len(suffix)) + suffix
	}
	return map[string]any{
		"username":     "u" + suffix,
		"email":        "u" + suffix + "@x.test",
		"display_name": "Test " + suffix,
		"password":     "Password123!",
	}
}

// --- POST /v1/auth/register ----------------------------------------------

func TestRegister_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)

	resp := post(t, c, h.Server.URL+"/v1/auth/register", validRegisterBody(t))
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	assertRequestIDEcho(t, resp)

	// Body must NOT include password_hash (§4.10 no-leak rule).
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "password_hash") || strings.Contains(string(raw), "password") {
		t.Fatalf("response leaked sensitive field: %s", raw)
	}
	// Must include user.id, user.username, user.email.
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	user, ok := got["user"].(map[string]any)
	if !ok {
		t.Fatalf("user field missing or wrong type: %+v", got)
	}
	for _, k := range []string{"id", "username", "email", "display_name", "color_scheme", "role"} {
		if _, ok := user[k]; !ok {
			t.Errorf("user.%s missing", k)
		}
	}

	// Cookie should now be set in the jar.
	srvURL := h.Server.URL
	jarCookies := c.Jar.Cookies(mustParseURL(t, srvURL))
	var sawSession bool
	for _, ck := range jarCookies {
		if ck.Name == "wakeup_session" {
			sawSession = true
		}
	}
	if !sawSession {
		t.Fatalf("wakeup_session cookie not set after register")
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)

	body := validRegisterBody(t)
	resp1 := post(t, c, h.Server.URL+"/v1/auth/register", body)
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first register status=%d", resp1.StatusCode)
	}

	// Same username, different email → CONFLICT.
	body2 := validRegisterBody(t)
	body2["username"] = body["username"]
	c2 := h.HTTPClient(t)
	resp2 := post(t, c2, h.Server.URL+"/v1/auth/register", body2)
	t.Cleanup(func() { _ = resp2.Body.Close() })
	assertCode(t, resp2, http.StatusConflict, apierror.CodeConflict)
}

func TestRegister_Validation(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)

	cases := []struct {
		name    string
		mutate  func(b map[string]any)
		wantTag string // expected error.fields[0].field
	}{
		{"missing_username", func(b map[string]any) { delete(b, "username") }, "username"},
		{"missing_email", func(b map[string]any) { delete(b, "email") }, "email"},
		{"missing_display_name", func(b map[string]any) { delete(b, "display_name") }, "display_name"},
		{"missing_password", func(b map[string]any) { delete(b, "password") }, "password"},
		{"username_too_short", func(b map[string]any) { b["username"] = "ab" }, "username"},
		{"username_too_long", func(b map[string]any) { b["username"] = strings.Repeat("a", 33) }, "username"},
		{"username_non_alphanum", func(b map[string]any) { b["username"] = "caden_lund" }, "username"},
		{"email_invalid", func(b map[string]any) { b["email"] = "not-an-email" }, "email"},
		{"display_name_empty", func(b map[string]any) { b["display_name"] = "" }, "display_name"},
		{"display_name_too_long", func(b map[string]any) { b["display_name"] = strings.Repeat("x", 65) }, "display_name"},
		{"password_too_short", func(b map[string]any) { b["password"] = "short" }, "password"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := validRegisterBody(t)
			tc.mutate(body)
			resp := post(t, c, h.Server.URL+"/v1/auth/register", body)
			t.Cleanup(func() { _ = resp.Body.Close() })
			if resp.StatusCode != http.StatusUnprocessableEntity {
				rb, _ := io.ReadAll(resp.Body)
				t.Fatalf("%s: status=%d body=%s", tc.name, resp.StatusCode, rb)
			}
			env := decodeErr(t, resp.Body)
			if env.Error.Code != apierror.CodeValidation {
				t.Errorf("Code=%q want VALIDATION_FAILED", env.Error.Code)
			}
			if len(env.Error.Fields) == 0 {
				t.Fatalf("expected at least one Field, got %+v", env.Error)
			}
			var sawTarget bool
			for _, fe := range env.Error.Fields {
				if fe.Field == tc.wantTag {
					sawTarget = true
					break
				}
			}
			if !sawTarget {
				t.Errorf("expected a Field for %q, got %+v", tc.wantTag, env.Error.Fields)
			}
			assertRequestIDEcho(t, resp)
		})
	}
}

func TestRegister_BadRequest(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)

	cases := []struct {
		name string
		body any
	}{
		{"empty_body", ""},
		{"malformed_json", "{not-json"},
		{"unknown_field", `{"username":"u123","email":"e@x.test","display_name":"D","password":"Password123!","extra":"nope"}`},
		{"trailing_garbage", `{"username":"u123","email":"e@x.test","display_name":"D","password":"Password123!"}{"more":1}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := post(t, c, h.Server.URL+"/v1/auth/register", tc.body)
			t.Cleanup(func() { _ = resp.Body.Close() })
			if resp.StatusCode != http.StatusBadRequest {
				rb, _ := io.ReadAll(resp.Body)
				t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
			}
			env := decodeErr(t, resp.Body)
			if env.Error.Code != apierror.CodeBadRequest {
				t.Errorf("Code=%q want BAD_REQUEST", env.Error.Code)
			}
		})
	}
}

func TestRegister_PayloadTooLarge(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)

	// >1 MiB body — display_name padded to push over the cap.
	body := validRegisterBody(t)
	body["display_name"] = strings.Repeat("x", 1<<20+128)
	resp := post(t, c, h.Server.URL+"/v1/auth/register", body)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusRequestEntityTooLarge, apierror.CodePayloadTooLarge)
}

// --- POST /v1/auth/login -------------------------------------------------

func TestLogin_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)

	body := validRegisterBody(t)
	c := h.HTTPClient(t)
	r := post(t, c, h.Server.URL+"/v1/auth/register", body)
	_ = r.Body.Close()
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("setup register status=%d", r.StatusCode)
	}

	c2 := h.HTTPClient(t)
	loginBody := map[string]any{"identifier": body["username"], "password": body["password"]}
	resp := post(t, c2, h.Server.URL+"/v1/auth/login", loginBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
	assertRequestIDEcho(t, resp)

	// Cookie must be in the jar of the second client now.
	srvURL := mustParseURL(t, h.Server.URL)
	var sawSession bool
	for _, ck := range c2.Jar.Cookies(srvURL) {
		if ck.Name == "wakeup_session" && ck.Value != "" {
			sawSession = true
		}
	}
	if !sawSession {
		t.Fatalf("wakeup_session cookie missing after login")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)

	body := validRegisterBody(t)
	c := h.HTTPClient(t)
	_ = post(t, c, h.Server.URL+"/v1/auth/register", body).Body.Close()

	c2 := h.HTTPClient(t)
	resp := post(t, c2, h.Server.URL+"/v1/auth/login", map[string]any{
		"identifier": body["username"],
		"password":   "wrong-password",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestLogin_UnknownUser(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/login", map[string]any{
		"identifier": "noone-here-12345",
		"password":   "Password123!",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestLogin_Validation(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/login", map[string]any{})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestLogin_BadRequest(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/login", "{not json")
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// --- POST /v1/auth/logout ------------------------------------------------

func TestLogout_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/logout", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}

	// /me must now be 401.
	r2, err := c.Get(h.Server.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	t.Cleanup(func() { _ = r2.Body.Close() })
	if r2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-logout /me status=%d, want 401", r2.StatusCode)
	}
}

func TestLogout_NoSessionIsIdempotent(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/logout", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", resp.StatusCode)
	}
}

// --- POST /v1/auth/logout-all --------------------------------------------

func TestLogoutAll_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/logout-all", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
}

func TestLogoutAll_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/logout-all", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- GET /v1/auth/me -----------------------------------------------------

func TestMe_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, u := h.AuthClient(t)

	resp, err := c.Get(h.Server.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
	assertRequestIDEcho(t, resp)
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "password_hash") {
		t.Fatalf("response leaked password_hash: %s", body)
	}
	var me map[string]any
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me["username"] != u.Username {
		t.Errorf("username = %v, want %s", me["username"], u.Username)
	}
}

func TestMe_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestMe_GarbageSessionCookie(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)

	// Inject a junk cookie. scs treats unknown tokens as "no session" and
	// /me returns 401 — same behavior as no cookie at all.
	srvURL := mustParseURL(t, h.Server.URL)
	c.Jar.SetCookies(srvURL, []*http.Cookie{{
		Name:   "wakeup_session",
		Value:  "garbage-not-a-real-token",
		Path:   "/",
		Secure: true,
	}})
	resp, err := c.Get(h.Server.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- POST /v1/auth/password-reset/request --------------------------------

func TestRequestPasswordReset_Always204(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)

	body := validRegisterBody(t)
	c := h.HTTPClient(t)
	_ = post(t, c, h.Server.URL+"/v1/auth/register", body).Body.Close()

	c2 := h.HTTPClient(t)

	// Known email → 204, fake mailer captures.
	resp1 := post(t, c2, h.Server.URL+"/v1/auth/password-reset/request",
		map[string]any{"email": body["email"]})
	t.Cleanup(func() { _ = resp1.Body.Close() })
	if resp1.StatusCode != http.StatusNoContent {
		t.Fatalf("known: status=%d", resp1.StatusCode)
	}
	if got := len(h.Mailer.Sent); got != 1 {
		t.Fatalf("Mailer.Sent len = %d, want 1 (after known email)", got)
	}

	// Unknown email → still 204, NO mail captured.
	h.Mailer.Reset()
	resp2 := post(t, c2, h.Server.URL+"/v1/auth/password-reset/request",
		map[string]any{"email": "nobody-here@x.test"})
	t.Cleanup(func() { _ = resp2.Body.Close() })
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("unknown: status=%d", resp2.StatusCode)
	}
	if got := len(h.Mailer.Sent); got != 0 {
		t.Fatalf("Mailer.Sent len = %d, want 0 (after unknown email)", got)
	}
}

func TestRequestPasswordReset_Validation(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/password-reset/request",
		map[string]any{"email": "not-an-email"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestRequestPasswordReset_BadRequest(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/password-reset/request", "{not json")
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// --- POST /v1/auth/password-reset/confirm --------------------------------

func TestConfirmPasswordReset_BadToken(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/auth/password-reset/confirm",
		map[string]any{"token": "definitely-not-a-real-token", "new_password": "Password123!"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestConfirmPasswordReset_Validation(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing_token", map[string]any{"new_password": "Password123!"}},
		{"missing_new_password", map[string]any{"token": "abc"}},
		{"new_password_too_short", map[string]any{"token": "abc", "new_password": "short"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := post(t, c, h.Server.URL+"/v1/auth/password-reset/confirm", tc.body)
			t.Cleanup(func() { _ = resp.Body.Close() })
			assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
		})
	}
}

// --- end-to-end happy path: register → confirm reset using the captured
// mailer token recreates the live flow without hitting Resend.
func TestPasswordReset_EndToEnd(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)

	body := validRegisterBody(t)
	c := h.HTTPClient(t)
	_ = post(t, c, h.Server.URL+"/v1/auth/register", body).Body.Close()

	// Request reset → Mailer captures the raw token.
	c2 := h.HTTPClient(t)
	resp := post(t, c2, h.Server.URL+"/v1/auth/password-reset/request",
		map[string]any{"email": body["email"]})
	_ = resp.Body.Close()
	if len(h.Mailer.Sent) != 1 {
		t.Fatalf("Mailer.Sent = %d, want 1", len(h.Mailer.Sent))
	}
	rawToken := h.Mailer.Sent[0].Token
	if rawToken == "" {
		t.Fatal("captured token is empty")
	}

	// Confirm with the captured token + new password → 204.
	c3 := h.HTTPClient(t)
	resp2 := post(t, c3, h.Server.URL+"/v1/auth/password-reset/confirm",
		map[string]any{"token": rawToken, "new_password": "Brand-New-Password-1"})
	t.Cleanup(func() { _ = resp2.Body.Close() })
	if resp2.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp2.Body)
		t.Fatalf("confirm status=%d body=%s", resp2.StatusCode, rb)
	}

	// Login with the new password works; old password no longer does.
	c4 := h.HTTPClient(t)
	loginNew := post(t, c4, h.Server.URL+"/v1/auth/login",
		map[string]any{"identifier": body["username"], "password": "Brand-New-Password-1"})
	_ = loginNew.Body.Close()
	if loginNew.StatusCode != http.StatusOK {
		t.Fatalf("login with new password status=%d", loginNew.StatusCode)
	}
	c5 := h.HTTPClient(t)
	loginOld := post(t, c5, h.Server.URL+"/v1/auth/login",
		map[string]any{"identifier": body["username"], "password": body["password"]})
	t.Cleanup(func() { _ = loginOld.Body.Close() })
	if loginOld.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login with old password status=%d, want 401", loginOld.StatusCode)
	}
}

// --- DTO no-leak: every response shape MUST omit password_hash etc. -----

func TestAuthDTOs_NoLeak(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp, err := c.Get(h.Server.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, _ := io.ReadAll(resp.Body)
	for _, banned := range []string{"password_hash", "deleted_at", "PasswordHash"} {
		if strings.Contains(string(body), banned) {
			t.Errorf("response leaked %q: %s", banned, body)
		}
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}
