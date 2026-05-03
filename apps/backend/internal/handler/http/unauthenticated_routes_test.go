// Unauthenticated-route sweep — for every authenticated endpoint, an
// HTTP call without a session must fast-fail with 401 from the
// CurrentUser guard. The success-path tests already cover happy + 4xx
// branches, but the 401 branch is uncovered for many handlers because
// nothing routinely tests "no session" on every route.
//
// Aggregating the cases here keeps the §13.8 audit honest without
// scattering 30 near-identical tests across 10 files.
package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// TestUnauthenticatedSweep verifies every authenticated route returns
// 401 + apierror.CodeUnauthorized when invoked without a session
// cookie. Each entry pairs a method + path; the body is fixed at
// empty/nullable JSON because we expect to short-circuit at the auth
// guard before any decoder touches it.
func TestUnauthenticatedSweep(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)

	type route struct {
		method, path, body, ctype string
	}
	routes := []route{
		// logout-all requires auth (logout itself is fine without a session).
		{http.MethodPost, "/v1/auth/logout-all", "", ""},
		{http.MethodGet, "/v1/auth/me", "", ""},

		// Users — every endpoint authenticated.
		{http.MethodGet, "/v1/users", "", ""},
		{http.MethodGet, "/v1/users/00000000-0000-0000-0000-000000000000", "", ""},
		{http.MethodPatch, "/v1/users/me", `{}`, "application/json"},
		{http.MethodDelete, "/v1/users/me", "", ""},
		{http.MethodGet, "/v1/users/me/notifications", "", ""},

		// Conversations — every endpoint authenticated.
		{http.MethodGet, "/v1/conversations", "", ""},
		{http.MethodGet, "/v1/conversations/00000000-0000-0000-0000-000000000000", "", ""},
		{http.MethodPost, "/v1/conversations", `{"type":"direct","member_ids":["00000000-0000-0000-0000-000000000000"]}`, "application/json"},
		{http.MethodPatch, "/v1/conversations/00000000-0000-0000-0000-000000000000", `{}`, "application/json"},
		{http.MethodDelete, "/v1/conversations/00000000-0000-0000-0000-000000000000/members/me", "", ""},
		{http.MethodPost, "/v1/conversations/00000000-0000-0000-0000-000000000000/members", `{"user_ids":[]}`, "application/json"},
		{http.MethodDelete, "/v1/conversations/00000000-0000-0000-0000-000000000000/members/00000000-0000-0000-0000-000000000000", "", ""},
		{http.MethodPost, "/v1/conversations/00000000-0000-0000-0000-000000000000/read", `{"message_id":"00000000-0000-0000-0000-000000000000"}`, "application/json"},

		// Friends.
		{http.MethodGet, "/v1/friends", "", ""},
		{http.MethodGet, "/v1/friends/requests", "", ""},
		{http.MethodPost, "/v1/friends/requests", `{"username":"x"}`, "application/json"},
		{http.MethodPost, "/v1/friends/requests/00000000-0000-0000-0000-000000000000/accept", "", ""},
		{http.MethodPost, "/v1/friends/requests/00000000-0000-0000-0000-000000000000/decline", "", ""},
		{http.MethodDelete, "/v1/friends/00000000-0000-0000-0000-000000000000", "", ""},
		{http.MethodPost, "/v1/friends/00000000-0000-0000-0000-000000000000/block", "", ""},
		{http.MethodDelete, "/v1/friends/00000000-0000-0000-0000-000000000000/block", "", ""},

		// Messages.
		{http.MethodGet, "/v1/conversations/00000000-0000-0000-0000-000000000000/messages", "", ""},
		{http.MethodPost, "/v1/conversations/00000000-0000-0000-0000-000000000000/messages", `{"body":"hi"}`, "application/json"},
		{http.MethodPatch, "/v1/messages/00000000-0000-0000-0000-000000000000", `{"body":"hi"}`, "application/json"},
		{http.MethodDelete, "/v1/messages/00000000-0000-0000-0000-000000000000", "", ""},
		{http.MethodGet, "/v1/messages/00000000-0000-0000-0000-000000000000/reads", "", ""},

		// Attachments.
		{http.MethodGet, "/v1/attachments/00000000-0000-0000-0000-000000000000", "", ""},

		// Devices.
		{http.MethodPost, "/v1/devices", `{"expo_token":"x","platform":"ios"}`, "application/json"},
		{http.MethodDelete, "/v1/devices/00000000-0000-0000-0000-000000000000", "", ""},

		// Presence + rooms.
		{http.MethodGet, "/v1/presence/friends", "", ""},
		{http.MethodGet, "/v1/widget/friends", "", ""},
		{http.MethodPost, "/v1/presence/status", `{"status":"online"}`, "application/json"},
		{http.MethodGet, "/v1/conversations/00000000-0000-0000-0000-000000000000/room", "", ""},
		{http.MethodPost, "/v1/conversations/00000000-0000-0000-0000-000000000000/room/join", "", ""},
		{http.MethodPost, "/v1/conversations/00000000-0000-0000-0000-000000000000/room/leave", "", ""},
	}

	for _, r := range routes {
		r := r
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequest(r.method, h.Server.URL+r.path, strings.NewReader(r.body))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if r.ctype != "" {
				req.Header.Set("Content-Type", r.ctype)
			}
			resp, err := c.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 for unauthenticated %s %s", resp.StatusCode, r.method, r.path)
				return
			}
			// Decode the body and verify the apierror envelope carries
			// the UNAUTHORIZED code — a regression that swaps in a
			// different sentinel (FORBIDDEN, AUTH_REQUIRED) wouldn't
			// trip the status-only assertion above.
			var env struct {
				Error apierror.Error `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				t.Errorf("decode body: %v", err)
				return
			}
			if env.Error.Code != apierror.CodeUnauthorized {
				t.Errorf("code = %q, want %q for %s %s",
					env.Error.Code, apierror.CodeUnauthorized, r.method, r.path)
			}
		})
	}
}
