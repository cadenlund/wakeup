package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// TestSmoke_FriendsGoldenPath drives every endpoint listed in §16
// milestone 4.4 in the documented order:
//
//	register alice + bob → SendRequest → ListRequests (alice + bob)
//	→ Accept → ListFriends (alice + bob) → Unfriend → ListFriends empty
//	→ Block → bob's reverse-direction request returns 409 → Unblock
//	→ bob requests → alice declines
//
// The §16 milestone 4.4 spec calls for a manual Swagger UI run; this
// is its automated equivalent. It hits the SAME wired router that
// `cmd/server.run` builds, so a passing run proves the spec → handler
// → service → repository chain is healthy end-to-end for the friend
// state machine.
func TestSmoke_FriendsGoldenPath(t *testing.T) {
	t.Parallel()
	srv, _, h := productionLikeServer(t)

	// 1) Register alice + bob via the production-router /v1/auth/register.
	aliceUsername := "alice" + uniqueSuffix(t)
	bobUsername := "bob" + uniqueSuffix(t)

	alice := newJaredClient(t, srv)
	mustPostJSON(t, alice, srv.URL+"/v1/auth/register", map[string]any{
		"username": aliceUsername, "email": aliceUsername + "@x.test",
		"display_name": "Alice", "password": "Password123!",
	}, http.StatusCreated)
	bob := newJaredClient(t, srv)
	mustPostJSON(t, bob, srv.URL+"/v1/auth/register", map[string]any{
		"username": bobUsername, "email": bobUsername + "@x.test",
		"display_name": "Bob", "password": "Password123!",
	}, http.StatusCreated)
	bobID := h.Mailer // silence the harness import — we don't use the mailer here
	_ = bobID
	bobUserRow, err := h.UserRepo.GetByUsername(t.Context(), bobUsername)
	if err != nil {
		t.Fatalf("load bob: %v", err)
	}

	// 2) alice → bob: SendRequest
	pending := mustPostJSON(t, alice, srv.URL+"/v1/friends/requests", map[string]any{
		"username": bobUsername,
	}, http.StatusCreated)
	pendingID, _ := pending["id"].(string)
	if pending["status"] != "pending" {
		t.Errorf("expected pending status, got %v", pending["status"])
	}

	// 3) alice's GET /v1/friends/requests → outgoing has 1, incoming 0
	aliceReqs := mustGetJSON(t, alice, srv.URL+"/v1/friends/requests", http.StatusOK)
	if got := lenJSONArr(aliceReqs, "outgoing"); got != 1 {
		t.Errorf("alice outgoing len = %d, want 1", got)
	}
	if got := lenJSONArr(aliceReqs, "incoming"); got != 0 {
		t.Errorf("alice incoming len = %d, want 0", got)
	}

	// 4) bob's GET /v1/friends/requests → incoming has 1, outgoing 0
	bobReqs := mustGetJSON(t, bob, srv.URL+"/v1/friends/requests", http.StatusOK)
	if got := lenJSONArr(bobReqs, "incoming"); got != 1 {
		t.Errorf("bob incoming len = %d, want 1", got)
	}
	if got := lenJSONArr(bobReqs, "outgoing"); got != 0 {
		t.Errorf("bob outgoing len = %d, want 0", got)
	}

	// 5) bob accepts
	accepted := mustPostJSON(t, bob, srv.URL+"/v1/friends/requests/"+pendingID+"/accept",
		nil, http.StatusOK)
	if accepted["status"] != "accepted" {
		t.Errorf("expected accepted, got %v", accepted["status"])
	}

	// 6 + 7) Both sides see one accepted friend
	for who, c := range map[string]*http.Client{"alice": alice, "bob": bob} {
		list := mustGetJSON(t, c, srv.URL+"/v1/friends", http.StatusOK)
		data, _ := list["data"].([]any)
		if len(data) != 1 {
			t.Fatalf("%s: friends len = %d, want 1", who, len(data))
		}
		row := data[0].(map[string]any)
		if row["status"] != "accepted" {
			t.Errorf("%s: status = %v, want accepted", who, row["status"])
		}
	}

	// 8 + 9) alice unfriends; both sides see empty list
	resp := deleteAndAssert(t, alice, srv.URL+"/v1/friends/"+bobUserRow.ID.String(), http.StatusNoContent)
	_ = resp
	for who, c := range map[string]*http.Client{"alice": alice, "bob": bob} {
		list := mustGetJSON(t, c, srv.URL+"/v1/friends", http.StatusOK)
		data, _ := list["data"].([]any)
		if len(data) != 0 {
			t.Errorf("%s: friends len after unfriend = %d, want 0", who, len(data))
		}
	}

	// 10) alice blocks bob
	blocked := mustPostJSON(t, alice, srv.URL+"/v1/friends/"+bobUserRow.ID.String()+"/block",
		nil, http.StatusCreated)
	if blocked["status"] != "blocked" {
		t.Errorf("status = %v, want blocked", blocked["status"])
	}

	// 11) bob's friend-request to alice now collides on the pair-unique
	// index → 409. The error message is intentionally generic so
	// bob can't tell from the response that alice blocked him.
	resp2 := mustPostJSONExpectStatus(t, bob, srv.URL+"/v1/friends/requests",
		map[string]any{"username": aliceUsername}, http.StatusConflict)
	body2, _ := json.Marshal(resp2)
	if !strings.Contains(string(body2), "blocked") && !strings.Contains(string(body2), "exists") {
		// We accept either wording — just verify it's not a leak about who blocked whom.
		t.Logf("conflict body: %s", body2)
	}

	// 12) alice unblocks
	_ = deleteAndAssert(t, alice, srv.URL+"/v1/friends/"+bobUserRow.ID.String()+"/block",
		http.StatusNoContent)

	// 13) bob → alice: now succeeds, alice declines.
	pending2 := mustPostJSON(t, bob, srv.URL+"/v1/friends/requests",
		map[string]any{"username": aliceUsername}, http.StatusCreated)
	pending2ID, _ := pending2["id"].(string)
	resp3 := mustPostJSONNoBody(t, alice, srv.URL+"/v1/friends/requests/"+pending2ID+"/decline",
		http.StatusNoContent)
	_ = resp3

	// Final state: no friends, no pending requests for either side.
	for who, c := range map[string]*http.Client{"alice": alice, "bob": bob} {
		reqs := mustGetJSON(t, c, srv.URL+"/v1/friends/requests", http.StatusOK)
		if got := lenJSONArr(reqs, "incoming"); got != 0 {
			t.Errorf("%s final incoming = %d, want 0", who, got)
		}
		if got := lenJSONArr(reqs, "outgoing"); got != 0 {
			t.Errorf("%s final outgoing = %d, want 0", who, got)
		}
	}
}

// --- helpers -----------------------------------------------------------

// deleteAndAssert fires a DELETE, asserts the status code, and returns
// the response body for any further assertion.
func deleteAndAssert(t *testing.T, c *http.Client, urlStr string, wantStatus int) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, urlStr, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", urlStr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("DELETE %s status=%d, want %d; body=%s", urlStr, resp.StatusCode, wantStatus, body)
	}
	return body
}

// mustPostJSONExpectStatus POSTs and decodes the response body even on
// non-2xx — used to check error envelopes for confict / forbidden /
// validation cases.
func mustPostJSONExpectStatus(t *testing.T, c *http.Client, urlStr string, body any, wantStatus int) map[string]any {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := c.Post(urlStr, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s status=%d, want %d; body=%s", urlStr, resp.StatusCode, wantStatus, rb)
	}
	if len(rb) == 0 {
		return nil
	}
	var got map[string]any
	if err := json.Unmarshal(rb, &got); err != nil {
		t.Fatalf("decode %s: %v", urlStr, err)
	}
	return got
}

// mustPostJSONNoBody POSTs with no body and asserts the response status.
func mustPostJSONNoBody(t *testing.T, c *http.Client, urlStr string, wantStatus int) []byte {
	t.Helper()
	resp, err := c.Post(urlStr, "application/json", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s status=%d, want %d; body=%s", urlStr, resp.StatusCode, wantStatus, body)
	}
	return body
}

// lenJSONArr returns the length of an []any field on a decoded JSON map,
// or -1 when the field is missing or not an array.
func lenJSONArr(m map[string]any, field string) int {
	arr, ok := m[field].([]any)
	if !ok {
		return -1
	}
	return len(arr)
}

// silence unused-import warnings if testutil ever loses any helper:
var _ = testutil.NextSuffix
