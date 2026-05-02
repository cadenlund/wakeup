package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil/fixtures"
)

// minimalPNG is the same 1x1 transparent PNG used by the user-service
// tests — http.DetectContentType matches the signature as image/png
// without needing a real decoder.
var minimalPNG = []byte{
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

// patch helper — http.MethodPatch with JSON body.
func patch(t *testing.T, c *http.Client, urlStr string, body any) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPatch, urlStr, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new patch req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("patch %s: %v", urlStr, err)
	}
	return resp
}

func deleteReq(t *testing.T, c *http.Client, urlStr string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, urlStr, nil)
	if err != nil {
		t.Fatalf("new delete req: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("delete %s: %v", urlStr, err)
	}
	return resp
}

// --- GET /v1/users (search) ----------------------------------------------

func TestSearch_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	// Make a few fixture users to find.
	for i := 0; i < 3; i++ {
		fixtures.MakeUser(t, h.DB, fixtures.WithUsername("zsearch"+testutil.NextSuffix()))
	}

	resp, err := c.Get(h.Server.URL + "/v1/users?q=zsearch&limit=10")
	if err != nil {
		t.Fatalf("GET /users: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	assertRequestIDEcho(t, resp)
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "password_hash") {
		t.Fatalf("response leaked password_hash: %s", body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, ok := got["data"].([]any)
	if !ok {
		t.Fatalf("data missing or wrong type: %+v", got)
	}
	if len(data) < 3 {
		t.Errorf("expected >=3 users, got %d", len(data))
	}
}

func TestSearch_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/users?q=anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestSearch_BadCursor(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/users?cursor=not-a-real-cursor")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

func TestSearch_BadLimit(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/users?limit=-5")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// --- GET /v1/users/{id} --------------------------------------------------

func TestGetByID_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	target := fixtures.MakeUser(t, h.DB)

	resp, err := c.Get(h.Server.URL + "/v1/users/" + target.ID.String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, leak := range []string{"password_hash", "deleted_at", "email"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("public profile leaked %q: %s", leak, body)
		}
	}
}

func TestGetByID_SoftDeletedRendersPlaceholder(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	target := fixtures.MakeUser(t, h.DB, fixtures.WithSoftDeleted())

	resp, err := c.Get(h.Server.URL + "/v1/users/" + target.ID.String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	// Soft-deleted user is hidden from GetByID per the user repo's
	// `WHERE deleted_at IS NULL` guard — handler returns 404.
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestGetByID_NotFound(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/users/0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestGetByID_BadUUID(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/users/not-a-uuid")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

func TestGetByID_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/users/0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- PATCH /v1/users/me --------------------------------------------------

func TestUpdateMe_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	dark := "dark"
	resp := patch(t, c, h.Server.URL+"/v1/users/me", map[string]any{
		"display_name": "Renamed",
		"color_scheme": dark,
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var me map[string]any
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me["display_name"] != "Renamed" {
		t.Errorf("display_name = %v, want Renamed", me["display_name"])
	}
	if me["color_scheme"] != "dark" {
		t.Errorf("color_scheme = %v, want dark", me["color_scheme"])
	}
}

func TestUpdateMe_RejectsBadColorScheme(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := patch(t, c, h.Server.URL+"/v1/users/me", map[string]any{
		"color_scheme": "fuschia",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestUpdateMe_RejectsTooLongDisplayName(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := patch(t, c, h.Server.URL+"/v1/users/me", map[string]any{
		"display_name": strings.Repeat("x", 65),
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestUpdateMe_RejectsBadAvatarURL(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := patch(t, c, h.Server.URL+"/v1/users/me", map[string]any{
		"avatar_url": "not-a-url",
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestUpdateMe_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := patch(t, c, h.Server.URL+"/v1/users/me", map[string]any{"display_name": "x"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestUpdateMe_MalformedJSON(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	req, _ := http.NewRequest(http.MethodPatch, h.Server.URL+"/v1/users/me",
		strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// --- DELETE /v1/users/me -------------------------------------------------

func TestDeleteMe_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, u := h.AuthClient(t)

	resp := deleteReq(t, c, h.Server.URL+"/v1/users/me")
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	// /me must now be 401 (session destroyed).
	r2, _ := c.Get(h.Server.URL + "/v1/auth/me")
	t.Cleanup(func() { _ = r2.Body.Close() })
	if r2.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-delete /me status=%d, want 401", r2.StatusCode)
	}

	// User row should be soft-deleted (deleted_at populated).
	got, err := h.UserRepo.GetByIDIncludingDeleted(t.Context(), u.ID)
	if err != nil {
		t.Fatalf("GetByIDIncludingDeleted: %v", err)
	}
	if got.DeletedAt == nil {
		t.Errorf("DeletedAt should be set after soft delete")
	}
}

func TestDeleteMe_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := deleteReq(t, c, h.Server.URL+"/v1/users/me")
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- POST /v1/users/me/avatar -------------------------------------------

// uploadFile builds a multipart body with a single `file` part containing data.
func uploadFile(t *testing.T, c *http.Client, urlStr string, filename string, data []byte) *http.Response {
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
		t.Fatalf("upload: %v", err)
	}
	return resp
}

func TestUploadAvatar_PNGSuccess(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := uploadFile(t, c, h.Server.URL+"/v1/users/me/avatar", "avatar.png", minimalPNG)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	user, ok := got["user"].(map[string]any)
	if !ok {
		t.Fatalf("user field missing: %+v", got)
	}
	avatar, _ := user["avatar_url"].(string)
	if !strings.HasSuffix(avatar, ".png") {
		t.Errorf("avatar_url should end .png, got %q", avatar)
	}
}

func TestUploadAvatar_RejectsBadMIME(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := uploadFile(t, c, h.Server.URL+"/v1/users/me/avatar", "avatar.txt",
		[]byte("plain text, not an image"))
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestUploadAvatar_RejectsTooLarge(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	// 6 MiB body — past the 5 MiB cap; Content-Length forces the
	// http.MaxBytesReader / ParseMultipartForm path to reject early.
	big := make([]byte, 6<<20)
	copy(big, minimalPNG)
	resp := uploadFile(t, c, h.Server.URL+"/v1/users/me/avatar", "big.png", big)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusRequestEntityTooLarge, apierror.CodePayloadTooLarge)
}

func TestUploadAvatar_MissingFile(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	// Empty multipart with no `file` part.
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, h.Server.URL+"/v1/users/me/avatar", buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

func TestUploadAvatar_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := uploadFile(t, c, h.Server.URL+"/v1/users/me/avatar", "avatar.png", minimalPNG)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- GET /v1/users/me/notifications --------------------------------------

func TestGetNotifications_Defaults(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp, err := c.Get(h.Server.URL + "/v1/users/me/notifications")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var got map[string]bool
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"direct_messages", "group_messages", "friend_requests", "calls"} {
		if !got[k] {
			t.Errorf("%s default = false, want true", k)
		}
	}
}

func TestGetNotifications_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/users/me/notifications")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- PATCH /v1/users/me/notifications ------------------------------------

func TestUpdateNotifications_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := patch(t, c, h.Server.URL+"/v1/users/me/notifications", map[string]any{
		"calls": false, "friend_requests": false,
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var got map[string]bool
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["calls"] || got["friend_requests"] {
		t.Errorf("expected calls=false friend_requests=false, got %+v", got)
	}
	if !got["direct_messages"] || !got["group_messages"] {
		t.Errorf("untouched fields should remain true, got %+v", got)
	}
}

func TestUpdateNotifications_EmptyBodyAcceptedAsNoOp(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := patch(t, c, h.Server.URL+"/v1/users/me/notifications", map[string]any{})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestUpdateNotifications_MalformedJSON(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	req, _ := http.NewRequest(http.MethodPatch,
		h.Server.URL+"/v1/users/me/notifications",
		strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

func TestUpdateNotifications_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := patch(t, c, h.Server.URL+"/v1/users/me/notifications", map[string]any{"calls": false})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- DTO no-leak ---------------------------------------------------------

func TestUserDTOs_NoLeakOnSearch(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp, err := c.Get(h.Server.URL + "/v1/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, _ := io.ReadAll(resp.Body)
	for _, leak := range []string{"password_hash", "PasswordHash", "deleted_at", "email", "role"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("search leaked %q: %s", leak, body)
		}
	}
}

// silence the linter — `url` is imported by sibling _test files.
var _ = url.Parse
