package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSmoke_ConversationsGoldenPath drives every endpoint listed in
// §16 milestone 5.4 in the documented order:
//
//	register alice + bob + carol + dave
//	→ alice creates direct with bob → list shows it
//	→ alice creates group with bob+carol → list shows both
//	→ alice patches group name → reflected in GET
//	→ alice adds dave via members endpoint
//	→ alice removes dave (admin kick path)
//	→ bob leaves the group → bob's list omits it; alice still sees the group
//	→ alice leaves the direct → alice's list omits it; bob still sees it
//
// MarkRead's success path requires a real message id (FK added in
// migration 0005) and is exercised in Phase 6 once the message repo
// lands. The §12.5 matrix tests in conversation_handler_test.go cover
// the auth/membership boundaries on /read.
func TestSmoke_ConversationsGoldenPath(t *testing.T) {
	t.Parallel()
	srv, _, _ := productionLikeServer(t)

	// 1) Register alice + bob + carol + dave.
	aliceUsername := "alice" + uniqueSuffix(t)
	bobUsername := "bob" + uniqueSuffix(t)
	carolUsername := "carol" + uniqueSuffix(t)
	daveUsername := "dave" + uniqueSuffix(t)

	alice := registerSmoke(t, srv, aliceUsername)
	bob := registerSmoke(t, srv, bobUsername)
	carol := registerSmoke(t, srv, carolUsername)
	dave := registerSmoke(t, srv, daveUsername)

	bobID := whoami(t, srv, bob)
	carolID := whoami(t, srv, carol)
	daveID := whoami(t, srv, dave)

	// 2) alice creates a direct with bob.
	directRow := mustPostJSON(t, alice, srv.URL+"/v1/conversations", map[string]any{
		"type":       "direct",
		"member_ids": []string{bobID},
	}, http.StatusCreated)
	if directRow["type"] != "direct" {
		t.Errorf("type = %v, want direct", directRow["type"])
	}
	directID, _ := directRow["id"].(string)
	if directID == "" {
		t.Fatalf("missing direct id: %#v", directRow)
	}

	// 3) alice creates a group with bob + carol.
	groupRow := mustPostJSON(t, alice, srv.URL+"/v1/conversations", map[string]any{
		"type":       "group",
		"name":       "Wakeup Crew",
		"member_ids": []string{bobID, carolID},
	}, http.StatusCreated)
	if groupRow["type"] != "group" {
		t.Errorf("type = %v, want group", groupRow["type"])
	}
	groupID, _ := groupRow["id"].(string)
	if groupID == "" {
		t.Fatalf("missing group id: %#v", groupRow)
	}

	// 4) alice's list now has 2 rows.
	list := mustGetJSON(t, alice, srv.URL+"/v1/conversations", http.StatusOK)
	data, _ := list["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("alice list len = %d, want 2", len(data))
	}

	// 5) alice patches the group's name.
	patched := mustPatchJSON(t, alice, srv.URL+"/v1/conversations/"+groupID,
		map[string]any{"name": "Wakeup Renamed"}, http.StatusOK)
	if patched["name"] != "Wakeup Renamed" {
		t.Errorf("name = %v, want Wakeup Renamed", patched["name"])
	}

	// 6) alice adds dave to the group.
	added := mustPostJSON(t, alice, srv.URL+"/v1/conversations/"+groupID+"/members",
		map[string]any{"user_ids": []string{daveID}}, http.StatusOK)
	addedRows, _ := added["added"].([]any)
	if len(addedRows) != 1 {
		t.Errorf("added len = %d, want 1", len(addedRows))
	}

	// 7) alice removes dave (admin kicks).
	_ = deleteAndAssert(t, alice, srv.URL+"/v1/conversations/"+groupID+"/members/"+daveID,
		http.StatusNoContent)

	// 8) bob leaves the group; bob's list now omits it (just the direct remains).
	_ = deleteAndAssert(t, bob, srv.URL+"/v1/conversations/"+groupID, http.StatusNoContent)
	bobList := mustGetJSON(t, bob, srv.URL+"/v1/conversations", http.StatusOK)
	bobData, _ := bobList["data"].([]any)
	if len(bobData) != 1 {
		t.Fatalf("bob list len after leaving group = %d, want 1 (just the direct)", len(bobData))
	}
	if first, ok := bobData[0].(map[string]any); ok && first["id"] != directID {
		t.Errorf("bob's remaining row should be the direct (%s), got %v", directID, first["id"])
	}

	// 9) alice leaves the direct; alice's list now omits it but bob's
	// view is unchanged (still has the direct).
	_ = deleteAndAssert(t, alice, srv.URL+"/v1/conversations/"+directID, http.StatusNoContent)

	aliceList := mustGetJSON(t, alice, srv.URL+"/v1/conversations", http.StatusOK)
	aliceData, _ := aliceList["data"].([]any)
	if len(aliceData) != 1 {
		t.Errorf("alice list after leaving direct = %d, want 1 (the group)", len(aliceData))
	}
	bobListAfter := mustGetJSON(t, bob, srv.URL+"/v1/conversations", http.StatusOK)
	bobDataAfter, _ := bobListAfter["data"].([]any)
	if len(bobDataAfter) != 1 {
		t.Errorf("bob list len = %d, want 1 (still sees the direct)", len(bobDataAfter))
	}
}

// --- helpers -----------------------------------------------------------

// registerSmoke registers a fresh user via the wired router and returns
// the cookie-jared client. Used by the conversation smoke test for
// multi-identity flows.
func registerSmoke(t *testing.T, srv *httptest.Server, username string) *http.Client {
	t.Helper()
	c := newJaredClient(t, srv)
	mustPostJSON(t, c, srv.URL+"/v1/auth/register", map[string]any{
		"username":     username,
		"email":        username + "@x.test",
		"display_name": "Smoke",
		"password":     "Password123!",
	}, http.StatusCreated)
	return c
}

// whoami returns the authenticated user's id by hitting /v1/auth/me.
func whoami(t *testing.T, srv *httptest.Server, c *http.Client) string {
	t.Helper()
	resp, err := c.Get(srv.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/me status=%d body=%s", resp.StatusCode, body)
	}
	var me map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	id, _ := me["id"].(string)
	if id == "" {
		t.Fatalf("/me missing id: %#v", me)
	}
	return id
}
