package main

import (
	"net/http"
	"testing"
)

// TestSmoke_PresenceGoldenPath drives every endpoint listed in §16
// milestone 9.4 in the documented order:
//
//	register alice + bob + carol
//	→ alice + bob become friends (carol stays a stranger)
//	→ alice's GET /v1/presence/friends → bob row, defaults to offline
//	→ bob POSTs /v1/presence/status { sleeping } → 204
//	→ alice's GET /v1/presence/friends → bob row now sleeping
//	→ alice's GET /v1/widget/friends → bob row with profile + presence
//	→ alice POSTs /v1/presence/status { offline } → 422 (only online/sleeping
//	  are user-settable per §6.1)
//	→ carol's GET /v1/presence/friends → empty (no friends)
//
// This is the docs-equivalent of clicking through the §6.1 presence
// endpoints in Swagger UI.
func TestSmoke_PresenceGoldenPath(t *testing.T) {
	t.Parallel()
	srv, _, h := productionLikeServer(t)

	aliceUsername := "alice" + uniqueSuffix(t)
	bobUsername := "bob" + uniqueSuffix(t)
	carolUsername := "carol" + uniqueSuffix(t)

	alice := registerSmoke(t, srv, aliceUsername)
	bob := registerSmoke(t, srv, bobUsername)
	carol := registerSmoke(t, srv, carolUsername)

	aliceID := whoami(t, srv, alice)
	bobID := whoami(t, srv, bob)
	_ = aliceID
	_ = carol

	// 1) Bob requests Alice as a friend; Alice accepts.
	pending := mustPostJSON(t, bob, srv.URL+"/v1/friends/requests", map[string]any{
		"username": aliceUsername,
	}, http.StatusCreated)
	pendingID, ok := pending["id"].(string)
	if !ok || pendingID == "" {
		t.Fatalf("missing friend request id: %#v", pending)
	}
	_ = mustPostJSONNoBody(t, alice, srv.URL+"/v1/friends/requests/"+pendingID+"/accept", http.StatusOK)

	// 2) Alice's friends-presence list shows Bob as offline (no row yet).
	friends := mustGetJSON(t, alice, srv.URL+"/v1/presence/friends", http.StatusOK)
	data, _ := friends["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("alice friends-presence len = %d, want 1", len(data))
	}
	first, ok := data[0].(map[string]any)
	if !ok {
		t.Fatalf("alice friends-presence row is not a map: %#v", data[0])
	}
	if first["user_id"] != bobID {
		t.Errorf("user_id = %v, want %v", first["user_id"], bobID)
	}
	if first["status"] != "offline" {
		t.Errorf("status = %v, want offline", first["status"])
	}

	// 3) Bob sets sleeping → 204.
	_ = mustPostJSONExpectStatus(t, bob, srv.URL+"/v1/presence/status",
		map[string]any{"status": "sleeping"}, http.StatusNoContent)

	// 4) Alice re-fetches; Bob is now sleeping.
	friendsAfter := mustGetJSON(t, alice, srv.URL+"/v1/presence/friends", http.StatusOK)
	dataAfter, _ := friendsAfter["data"].([]any)
	if len(dataAfter) != 1 {
		t.Fatalf("alice friends-presence post-set len = %d, want 1", len(dataAfter))
	}
	row, ok := dataAfter[0].(map[string]any)
	if !ok {
		t.Fatalf("post-set row is not a map: %#v", dataAfter[0])
	}
	if row["status"] != "sleeping" {
		t.Errorf("status = %v, want sleeping", row["status"])
	}

	// 5) Widget endpoint embeds user profile + presence in one shape.
	widget := mustGetJSON(t, alice, srv.URL+"/v1/widget/friends", http.StatusOK)
	wData, _ := widget["data"].([]any)
	if len(wData) != 1 {
		t.Fatalf("widget data len = %d, want 1", len(wData))
	}
	wRow, ok := wData[0].(map[string]any)
	if !ok {
		t.Fatalf("widget row is not a map: %#v", wData[0])
	}
	user, ok := wRow["user"].(map[string]any)
	if !ok {
		t.Fatalf("widget row missing user object: %#v", wRow)
	}
	if user["id"] != bobID {
		t.Errorf("widget.user.id = %v, want %v", user["id"], bobID)
	}
	if user["display_name"] == "" {
		t.Errorf("widget.user.display_name is empty")
	}
	pres, ok := wRow["presence"].(map[string]any)
	if !ok {
		t.Fatalf("widget row missing presence object: %#v", wRow)
	}
	if pres["status"] != "sleeping" {
		t.Errorf("widget.presence.status = %v, want sleeping", pres["status"])
	}

	// 6) Alice tries to set 'offline' → 422 (server-managed, not user-settable).
	_ = mustPostJSONExpectStatus(t, alice, srv.URL+"/v1/presence/status",
		map[string]any{"status": "offline"}, http.StatusUnprocessableEntity)

	// 7) Carol (no friends) gets an empty list.
	carolFriends := mustGetJSON(t, carol, srv.URL+"/v1/presence/friends", http.StatusOK)
	carolData, _ := carolFriends["data"].([]any)
	if len(carolData) != 0 {
		t.Errorf("carol friends-presence len = %d, want 0", len(carolData))
	}

	// Sanity: harness exposed the presence service so a future test
	// can assert decay-sweeper behavior end-to-end via Run() rather
	// than waiting on the 30s tick.
	_ = h.PresenceSvc
}
