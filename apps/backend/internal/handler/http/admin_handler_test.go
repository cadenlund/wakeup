package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/admin"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// patchUser is a small wrapper around c.Do for PATCH /v1/admin/users/{id}.
// Errors from json.Marshal and http.NewRequest are surfaced as t.Fatalf
// so a malformed test body fails the test loudly instead of producing
// an unrelated downstream failure.
func patchUser(t *testing.T, c *http.Client, urlStr string, body any) *http.Response {
	t.Helper()
	bs, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("patchUser: marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, urlStr, strings.NewReader(string(bs)))
	if err != nil {
		t.Fatalf("patchUser: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	return resp
}

// --- GET /v1/admin/users -------------------------------------------------

func TestAdminListUsers_RequiresAdmin(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t) // role=user
	resp, err := c.Get(h.Server.URL + "/v1/admin/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusForbidden, apierror.CodeForbidden)
}

func TestAdminListUsers_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/admin/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

func TestAdminListUsers_HappyPath(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	// Seed a couple of normal users so the list isn't just the admin.
	_, _ = h.AuthClient(t)
	_, _ = h.AuthClient(t)

	resp, err := adminC.Get(h.Server.URL + "/v1/admin/users?limit=10")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	data, _ := got["data"].([]any)
	if len(data) < 3 {
		t.Errorf("expected at least 3 users (admin + 2), got %d", len(data))
	}
}

// --- GET /v1/admin/users/{id} -------------------------------------------

func TestAdminGetUser_FindsSoftDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)
	if err := h.UserRepo.SoftDelete(ctx, target.ID); err != nil {
		t.Fatalf("seed soft delete: %v", err)
	}

	resp, err := adminC.Get(h.Server.URL + "/v1/admin/users/" + target.ID.String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["deleted_at"] == nil {
		t.Errorf("expected deleted_at to be non-null in admin view: %+v", got)
	}
}

func TestAdminGetUser_BadIDReturns400(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	resp, err := adminC.Get(h.Server.URL + "/v1/admin/users/not-a-uuid")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// --- PATCH /v1/admin/users/{id} -----------------------------------------

func TestAdminPatchUser_PromoteWritesAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)

	resp := patchUser(t, adminC, h.Server.URL+"/v1/admin/users/"+target.ID.String(),
		map[string]any{"role": "admin"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["role"] != "admin" {
		t.Errorf("role = %v, want admin", got["role"])
	}

	// Audit row should be present — use the service's list.
	res, err := h.AdminSvc.ListAudit(ctx, admin.ListAuditParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(res.Entries) < 1 || res.Entries[0].Action != admin.ActionUserUpdateRole {
		t.Errorf("expected user.update_role audit row, got %+v", res.Entries)
	}
}

func TestAdminPatchUser_SoftDelete(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)

	resp := patchUser(t, adminC, h.Server.URL+"/v1/admin/users/"+target.ID.String(),
		map[string]any{"deleted_at": "2026-05-02T09:31:21.810Z"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["deleted_at"] == nil {
		t.Errorf("expected deleted_at populated in response: %+v", got)
	}
}

func TestAdminPatchUser_EmptyBodyRejected(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)

	resp := patchUser(t, adminC, h.Server.URL+"/v1/admin/users/"+target.ID.String(),
		map[string]any{})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

// `"deleted_at": null` is "restore me" — not supported in 12.5, so it
// should surface as a clear 422 rather than silently no-op.
func TestAdminPatchUser_ExplicitNullDeletedAtRejected(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)

	// json.Marshal of map[string]any{"deleted_at": nil} produces "null"
	// for the value, which is exactly the wire shape we want to reject.
	resp := patchUser(t, adminC, h.Server.URL+"/v1/admin/users/"+target.ID.String(),
		map[string]any{"deleted_at": nil})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

// Combined role + soft-delete in one PATCH commits in a single tx.
func TestAdminPatchUser_RoleAndDeleteAtomically(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)

	resp := patchUser(t, adminC, h.Server.URL+"/v1/admin/users/"+target.ID.String(),
		map[string]any{"role": "admin", "deleted_at": "2026-05-02T09:31:21.810Z"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["role"] != "admin" {
		t.Errorf("role = %v, want admin", got["role"])
	}
	if got["deleted_at"] == nil {
		t.Errorf("deleted_at should be populated: %+v", got)
	}
}

// --- POST /v1/admin/users/{id}/impersonate ------------------------------

func TestAdminImpersonate_HappyPath(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)

	resp := post(t, adminC, h.Server.URL+"/v1/admin/users/"+target.ID.String()+"/impersonate", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["id"] != target.ID.String() {
		t.Errorf("response id = %v, want target %v", got["id"], target.ID)
	}
	imp, _ := got["impersonated_by"].(map[string]any)
	if imp == nil {
		t.Fatalf("impersonated_by must be present: %+v", got)
	}
	if imp["id"] == nil || imp["id"] == "" {
		t.Errorf("impersonator.id missing: %+v", imp)
	}

	// Subsequent GET /v1/auth/me should now resolve to the target.
	meResp, err := adminC.Get(h.Server.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	if meResp == nil {
		t.Fatal("GET /me returned nil response")
	}
	t.Cleanup(func() { _ = meResp.Body.Close() })
	me := mustDecode(t, meResp.Body)
	if me["id"] != target.ID.String() {
		t.Errorf("after impersonation /me should return target; got %v", me["id"])
	}
	if _, ok := me["impersonated_by"].(map[string]any); !ok {
		t.Errorf("impersonated_by missing on /me during impersonation: %+v", me)
	}
}

func TestAdminImpersonate_SelfReturns422(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, adminUser := h.AdminClient(t)
	resp := post(t, adminC, h.Server.URL+"/v1/admin/users/"+adminUser.ID.String()+"/impersonate", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestAdminImpersonate_AnotherAdminReturns403(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, otherAdmin := h.AdminClient(t)
	resp := post(t, adminC, h.Server.URL+"/v1/admin/users/"+otherAdmin.ID.String()+"/impersonate", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusForbidden, apierror.CodeForbidden)
}

func TestAdminImpersonate_MissingTargetReturns404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	resp := post(t, adminC, h.Server.URL+"/v1/admin/users/"+uuid.New().String()+"/impersonate", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestAdminImpersonate_NonAdminReturns403(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	_, target := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/admin/users/"+target.ID.String()+"/impersonate", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusForbidden, apierror.CodeForbidden)
}

// --- POST /v1/admin/impersonate/end -------------------------------------

func TestAdminEndImpersonation_StartThenEndRoundtrip(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)

	// Start
	startResp := post(t, adminC, h.Server.URL+"/v1/admin/users/"+target.ID.String()+"/impersonate", nil)
	t.Cleanup(func() { _ = startResp.Body.Close() })
	if startResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start status=%d body=%s", startResp.StatusCode, body)
	}

	// End
	endResp := post(t, adminC, h.Server.URL+"/v1/admin/impersonate/end", nil)
	t.Cleanup(func() { _ = endResp.Body.Close() })
	if endResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(endResp.Body)
		t.Fatalf("end status=%d body=%s", endResp.StatusCode, body)
	}
	got := mustDecode(t, endResp.Body)
	if _, ok := got["impersonated_by"]; ok {
		t.Errorf("impersonated_by must be absent after End, got %+v", got)
	}

	// Subsequent /me should return admin again.
	meResp, err := adminC.Get(h.Server.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	t.Cleanup(func() { _ = meResp.Body.Close() })
	me := mustDecode(t, meResp.Body)
	if me["role"] != "admin" {
		t.Errorf("after End, /me should return admin, got %+v", me)
	}
}

func TestAdminEndImpersonation_IdempotentNoBookendIfNotImpersonating(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	resp := post(t, adminC, h.Server.URL+"/v1/admin/impersonate/end", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	res, err := h.AdminSvc.ListAudit(ctx, admin.ListAuditParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	for _, e := range res.Entries {
		if e.Action == admin.ActionImpersonateEnded {
			t.Errorf("End without active impersonation must NOT write a bookend, got %+v", e)
		}
	}
}

// --- GET /v1/admin/audit -----------------------------------------------

func TestAdminListAudit_HappyPath(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)
	_, target := h.AuthClient(t)

	// Seed an audit row by promoting target.
	patchResp := patchUser(t, adminC, h.Server.URL+"/v1/admin/users/"+target.ID.String(),
		map[string]any{"role": "admin"})
	t.Cleanup(func() { _ = patchResp.Body.Close() })

	resp, err := adminC.Get(h.Server.URL + "/v1/admin/audit?limit=10")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	data, _ := got["data"].([]any)
	if len(data) < 1 {
		t.Errorf("expected at least 1 audit entry, got %+v", got)
	}
}

// §12.4 BLOCKED_DURING_IMPERSONATION end-to-end coverage lives in
// cmd/server/router_test.go where the production middleware chain
// (including BlockDuringImpersonation) is wired. The harness here
// only mounts handler routes; it intentionally doesn't re-implement
// the full router middleware tower.

// Bad-query-param sweep for the paginated admin endpoints. q exceeding
// 200 chars + bad limit + bad cursor each trip a distinct guard
// inside ListUsers / ListAudit before any service call. The "bad q"
// case fires apierror.BadRequest in the handler; the limit/cursor
// cases come from pagination's parsers.
func TestAdminListUsers_BadQueryParams(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)

	cases := []struct {
		name     string
		path     string
		wantCode apierror.Code
	}{
		{"q over 200 chars", "/v1/admin/users?q=" + strings.Repeat("x", 201), apierror.CodeBadRequest},
		{"bad limit", "/v1/admin/users?limit=abc", apierror.CodeBadRequest},
		{"bad cursor", "/v1/admin/users?cursor=not-base64", apierror.CodeBadRequest},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, err := adminC.Get(h.Server.URL + tc.path)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			assertCode(t, resp, http.StatusBadRequest, tc.wantCode)
		})
	}
}

func TestAdminListAudit_BadQueryParams(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	adminC, _ := h.AdminClient(t)

	cases := []struct {
		name, path string
		wantCode   apierror.Code
	}{
		{"bad limit", "/v1/admin/audit?limit=abc", apierror.CodeBadRequest},
		{"bad cursor", "/v1/admin/audit?cursor=not-base64", apierror.CodeBadRequest},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, err := adminC.Get(h.Server.URL + tc.path)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			assertCode(t, resp, http.StatusBadRequest, tc.wantCode)
		})
	}
}
