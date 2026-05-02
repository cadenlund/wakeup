package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// minimalPNG is the same 1x1 transparent PNG used by the user-service
// tests. http.DetectContentType matches its 8-byte signature as
// image/png without needing a real decoder.
var smokeMinimalPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

// TestSmoke_GoldenPathFromSwagger drives every endpoint listed in §16
// milestone 3.10 in the documented order:
//
//	register → login → me → update profile (color_scheme=dark)
//	→ get/patch notifications → upload avatar
//	→ request reset → confirm reset → login w/ new password → logout
//
// The §16 milestone 3.10 spec calls for a manual Swagger UI run; this
// test is the automated equivalent. It hits the SAME wired router that
// `cmd/server.run` builds and exercises every payload Swagger UI's
// "Try it out" pre-fills, so a passing run proves the spec → handler
// → service → repo → storage chain is healthy end-to-end.
func TestSmoke_GoldenPathFromSwagger(t *testing.T) {
	t.Parallel()
	srv, c, h := productionLikeServer(t)

	// 1) register — payload mirrors RegisterRequest's `example:"..."` tags
	regBody := map[string]any{
		"username":     "smoke" + uniqueSuffix(t),
		"email":        "smoke" + uniqueSuffix(t) + "@x.test",
		"display_name": "Smoke Test",
		"password":     "Password123!",
	}
	mustPostJSON(t, c, srv.URL+"/v1/auth/register", regBody, http.StatusCreated)

	// 2) login — fresh client (forces login to set the cookie itself)
	loginC := newJaredClient(t, srv)
	mustPostJSON(t, loginC, srv.URL+"/v1/auth/login", map[string]any{
		"identifier": regBody["username"],
		"password":   regBody["password"],
	}, http.StatusOK)

	// 3) GET /v1/auth/me — should round-trip the cookie
	me := mustGetJSON(t, loginC, srv.URL+"/v1/auth/me", http.StatusOK)
	if me["username"] != regBody["username"] {
		t.Fatalf("/me username = %v, want %v", me["username"], regBody["username"])
	}
	if me["color_scheme"] != "system" {
		t.Errorf("default color_scheme = %v, want system", me["color_scheme"])
	}

	// 4) PATCH /v1/users/me — flip color_scheme to dark
	patched := mustPatchJSON(t, loginC, srv.URL+"/v1/users/me", map[string]any{
		"color_scheme": "dark",
	}, http.StatusOK)
	if patched["color_scheme"] != "dark" {
		t.Errorf("after PATCH color_scheme = %v, want dark", patched["color_scheme"])
	}

	// 5) GET /v1/users/me/notifications — defaults are all true
	notifs := mustGetJSON(t, loginC, srv.URL+"/v1/users/me/notifications", http.StatusOK)
	for _, k := range []string{"direct_messages", "group_messages", "friend_requests", "calls"} {
		if notifs[k] != true {
			t.Errorf("notifications.%s default = %v, want true", k, notifs[k])
		}
	}

	// 6) PATCH /v1/users/me/notifications — flip calls=false
	patchedN := mustPatchJSON(t, loginC, srv.URL+"/v1/users/me/notifications", map[string]any{
		"calls": false,
	}, http.StatusOK)
	if patchedN["calls"] != false {
		t.Errorf("notifications.calls = %v, want false", patchedN["calls"])
	}
	if patchedN["direct_messages"] != true {
		t.Errorf("notifications.direct_messages = %v, want unchanged true", patchedN["direct_messages"])
	}

	// 7) POST /v1/users/me/avatar — multipart upload of a 1x1 PNG
	avatarResp := mustUploadFile(t, loginC, srv.URL+"/v1/users/me/avatar",
		"avatar.png", smokeMinimalPNG, http.StatusOK)
	user, ok := avatarResp["user"].(map[string]any)
	if !ok {
		t.Fatalf("/avatar response missing `user`: %+v", avatarResp)
	}
	avatar, _ := user["avatar_url"].(string)
	if !strings.HasSuffix(avatar, ".png") {
		t.Errorf("avatar_url = %q, want .png suffix", avatar)
	}

	// 8) POST /v1/auth/password-reset/request — captured by the fake mailer
	h.Mailer.Reset()
	mustPostJSON(t, loginC, srv.URL+"/v1/auth/password-reset/request", map[string]any{
		"email": regBody["email"],
	}, http.StatusNoContent)
	if len(h.Mailer.Sent) != 1 {
		t.Fatalf("Mailer.Sent = %d, want 1", len(h.Mailer.Sent))
	}
	rawToken := h.Mailer.Sent[0].Token

	// 9) POST /v1/auth/password-reset/confirm — uses the captured token
	confirmC := newJaredClient(t, srv)
	mustPostJSON(t, confirmC, srv.URL+"/v1/auth/password-reset/confirm", map[string]any{
		"token":        rawToken,
		"new_password": "NewPassword456!",
	}, http.StatusNoContent)

	// 10) login with new password works
	postResetC := newJaredClient(t, srv)
	mustPostJSON(t, postResetC, srv.URL+"/v1/auth/login", map[string]any{
		"identifier": regBody["username"],
		"password":   "NewPassword456!",
	}, http.StatusOK)

	// 11) logout — idempotent 204
	mustPostJSON(t, postResetC, srv.URL+"/v1/auth/logout", nil, http.StatusNoContent)

	// 12) /me must now be 401 — session destroyed
	r, err := postResetC.Get(srv.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("post-logout GET /me: %v", err)
	}
	t.Cleanup(func() { _ = r.Body.Close() })
	if r.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("post-logout /me status = %d body=%s, want 401", r.StatusCode, body)
	}
}

// --- HTTP helpers --------------------------------------------------------

func mustPostJSON(t *testing.T, c *http.Client, urlStr string, body any, wantStatus int) map[string]any {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	resp, err := c.Post(urlStr, "application/json", rdr)
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return assertStatusAndDecode(t, resp, wantStatus, urlStr)
}

func mustPatchJSON(t *testing.T, c *http.Client, urlStr string, body any, wantStatus int) map[string]any {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, urlStr, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", urlStr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return assertStatusAndDecode(t, resp, wantStatus, urlStr)
}

func mustGetJSON(t *testing.T, c *http.Client, urlStr string, wantStatus int) map[string]any {
	t.Helper()
	resp, err := c.Get(urlStr)
	if err != nil {
		t.Fatalf("GET %s: %v", urlStr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return assertStatusAndDecode(t, resp, wantStatus, urlStr)
}

func mustUploadFile(t *testing.T, c *http.Client, urlStr, filename string, data []byte, wantStatus int) map[string]any {
	t.Helper()
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, urlStr, buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return assertStatusAndDecode(t, resp, wantStatus, urlStr)
}

func assertStatusAndDecode(t *testing.T, resp *http.Response, wantStatus int, urlStr string) map[string]any {
	t.Helper()
	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s status = %d, want %d; body=%s", urlStr, resp.StatusCode, wantStatus, body)
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	var got map[string]any
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v\nbody=%s", urlStr, err, body)
	}
	return got
}

// newJaredClient builds a fresh cookie-jared http.Client trusting the
// test server's TLS cert. Used between flow steps where we want a
// distinct cookie identity (e.g. the reset-confirm step shouldn't share
// state with the post-reset login step).
func newJaredClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	tr, ok := srv.Client().Transport.(*http.Transport)
	if !ok || tr == nil {
		tr = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	} else {
		tr = tr.Clone()
	}
	return &http.Client{Transport: tr, Jar: jar}
}

// uniqueSuffix returns a process-unique alphanumeric suffix (re-exports
// the testutil counter through this file's API).
func uniqueSuffix(t *testing.T) string {
	t.Helper()
	suffix := testutil.NextSuffix()
	if len(suffix) < 8 {
		suffix = strings.Repeat("0", 8-len(suffix)) + suffix
	}
	return suffix
}
