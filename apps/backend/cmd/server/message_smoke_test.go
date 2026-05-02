package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestSmoke_MessagesGoldenPath drives every endpoint listed in §16
// milestone 6.4 in the documented order:
//
//	register alice + bob + carol
//	→ alice creates direct with bob → empty list at first
//	→ alice sends 3 messages to bob → bob's list (newest first) shows them
//	→ alice edits her first message → list reflects body update + edited_at
//	→ alice deletes her second message → still in list with body="" and is_deleted=true
//	→ bob sends a reply → list grows to 4 rows
//	→ alice creates a group with bob+carol, sends a message → carol can read it
//	→ alice tries to view bob's reply via direct as carol (non-member) → 404
//	→ alice GETs reads on her own message → empty list (nobody has marked it read yet)
//	→ alice tries q=fox after sending "the quick brown fox" → finds 1 row
//
// This is the docs-equivalent of clicking through Swagger UI by hand,
// but it runs in CI so regressions can't sneak past the next milestone.
func TestSmoke_MessagesGoldenPath(t *testing.T) {
	t.Parallel()
	srv, _, _ := productionLikeServer(t)

	// 1) Register alice + bob + carol.
	aliceUsername := "alice" + uniqueSuffix(t)
	bobUsername := "bob" + uniqueSuffix(t)
	carolUsername := "carol" + uniqueSuffix(t)

	alice := registerSmoke(t, srv, aliceUsername)
	bob := registerSmoke(t, srv, bobUsername)
	carol := registerSmoke(t, srv, carolUsername)

	bobID := whoami(t, srv, bob)
	carolID := whoami(t, srv, carol)

	// 2) alice creates a direct with bob.
	directRow := mustPostJSON(t, alice, srv.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []string{bobID},
	}, http.StatusCreated)
	directID, _ := directRow["id"].(string)
	if directID == "" {
		t.Fatalf("missing direct id: %#v", directRow)
	}

	// 3) Empty list at first.
	emptyList := mustGetJSON(t, alice, srv.URL+"/v1/conversations/"+directID+"/messages", http.StatusOK)
	if data, _ := emptyList["data"].([]any); len(data) != 0 {
		t.Fatalf("initial list should be empty, got %d", len(data))
	}

	// 4) alice sends 3 messages.
	bodies := []string{"first", "second", "third"}
	ids := make([]string, 0, len(bodies))
	for _, body := range bodies {
		row := mustPostJSON(t, alice, srv.URL+"/v1/conversations/"+directID+"/messages",
			map[string]any{"body": body}, http.StatusCreated)
		id, _ := row["id"].(string)
		if id == "" {
			t.Fatalf("missing message id for body=%q: %#v", body, row)
		}
		ids = append(ids, id)
	}

	// 5) bob lists the conversation, sees 3 rows newest-first ("third" first).
	bobList := mustGetJSON(t, bob, srv.URL+"/v1/conversations/"+directID+"/messages", http.StatusOK)
	bobData, _ := bobList["data"].([]any)
	if len(bobData) != 3 {
		t.Fatalf("bob list len = %d, want 3", len(bobData))
	}
	if first, ok := bobData[0].(map[string]any); ok && first["body"] != "third" {
		t.Errorf("bob's newest row body = %v, want third", first["body"])
	}

	// 6) alice edits her first message — body update + edited_at stamp.
	patched := mustPatchJSON(t, alice, srv.URL+"/v1/messages/"+ids[0],
		map[string]any{"body": "first edited"}, http.StatusOK)
	if patched["body"] != "first edited" {
		t.Errorf("edited body = %v, want first edited", patched["body"])
	}
	if patched["edited_at"] == nil {
		t.Errorf("edited_at should be set after PATCH")
	}

	// 7) alice deletes her second message — list still shows the row but
	// with body="" and is_deleted=true (§4.6 placeholder rendering).
	_ = deleteAndAssert(t, alice, srv.URL+"/v1/messages/"+ids[1], http.StatusNoContent)

	postDeleteList := mustGetJSON(t, alice, srv.URL+"/v1/conversations/"+directID+"/messages", http.StatusOK)
	postDeleteData, _ := postDeleteList["data"].([]any)
	if len(postDeleteData) != 3 {
		t.Fatalf("post-delete list len = %d, want 3 (deleted row stays as placeholder)", len(postDeleteData))
	}
	var deletedRow map[string]any
	for _, raw := range postDeleteData {
		row, _ := raw.(map[string]any)
		if row["id"] == ids[1] {
			deletedRow = row
			break
		}
	}
	if deletedRow == nil {
		t.Fatalf("deleted row %q not found in list", ids[1])
	}
	if deletedRow["body"] != "" {
		t.Errorf("deleted body = %v, want empty string", deletedRow["body"])
	}
	if !deletedRow["is_deleted"].(bool) {
		t.Errorf("is_deleted = false, want true")
	}

	// 8) bob replies → list grows to 4 rows.
	bobReply := mustPostJSON(t, bob, srv.URL+"/v1/conversations/"+directID+"/messages",
		map[string]any{"body": "bob's reply"}, http.StatusCreated)
	bobReplyID, _ := bobReply["id"].(string)
	if bobReplyID == "" {
		t.Fatalf("bob reply missing id: %#v", bobReply)
	}
	growing := mustGetJSON(t, alice, srv.URL+"/v1/conversations/"+directID+"/messages", http.StatusOK)
	growingData, _ := growing["data"].([]any)
	if len(growingData) != 4 {
		t.Errorf("after bob reply len = %d, want 4", len(growingData))
	}

	// 9) alice creates a group with bob + carol, sends a message; carol reads it.
	groupRow := mustPostJSON(t, alice, srv.URL+"/v1/conversations", map[string]any{
		"type": "group", "name": "Wakeup Crew", "member_ids": []string{bobID, carolID},
	}, http.StatusCreated)
	groupID, _ := groupRow["id"].(string)
	if groupID == "" {
		t.Fatalf("missing group id: %#v", groupRow)
	}
	groupMsg := mustPostJSON(t, alice, srv.URL+"/v1/conversations/"+groupID+"/messages",
		map[string]any{"body": "hello crew"}, http.StatusCreated)
	groupMsgID, _ := groupMsg["id"].(string)
	if groupMsgID == "" {
		t.Fatalf("group message missing id: %#v", groupMsg)
	}
	carolList := mustGetJSON(t, carol, srv.URL+"/v1/conversations/"+groupID+"/messages", http.StatusOK)
	if data, _ := carolList["data"].([]any); len(data) != 1 {
		t.Errorf("carol group list len = %d, want 1", len(data))
	}

	// 10) carol cannot list the alice<->bob direct (non-member → 404).
	carolPeek := mustGetJSON(t, carol, srv.URL+"/v1/conversations/"+directID+"/messages", http.StatusNotFound)
	if errBlock, _ := carolPeek["error"].(map[string]any); errBlock == nil {
		t.Errorf("carol peek should return error envelope, got %#v", carolPeek)
	}

	// 11) alice GETs reads on her group message — nobody marked it yet.
	reads := mustGetJSON(t, alice, srv.URL+"/v1/messages/"+groupMsgID+"/reads", http.StatusOK)
	if data, _ := reads["data"].([]any); len(data) != 0 {
		t.Errorf("reads len = %d, want 0", len(data))
	}

	// 12) Full-text search round-trip: alice sends a `fox` message in the
	// group, then `?q=fox` in List returns just that row.
	_ = mustPostJSON(t, alice, srv.URL+"/v1/conversations/"+groupID+"/messages",
		map[string]any{"body": "the quick brown fox"}, http.StatusCreated)
	_ = mustPostJSON(t, alice, srv.URL+"/v1/conversations/"+groupID+"/messages",
		map[string]any{"body": "lazy dog rests"}, http.StatusCreated)
	hit := mustGetJSON(t, alice, srv.URL+"/v1/conversations/"+groupID+"/messages?q=fox", http.StatusOK)
	hitData, _ := hit["data"].([]any)
	if len(hitData) != 1 {
		t.Errorf("q=fox len = %d, want 1", len(hitData))
	} else if first, ok := hitData[0].(map[string]any); ok {
		body, _ := first["body"].(string)
		if !strings.Contains(body, "fox") {
			t.Errorf("q=fox match body = %q, expected contains 'fox'", body)
		}
	}
}
