package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// patchJSON / deleteReq / mustGetMap helpers are reused across tests.

func patchJSON(t *testing.T, c *http.Client, urlStr string, body any) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, urlStr, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("patch %s: %v", urlStr, err)
	}
	return resp
}

func deleteReqHTTP(t *testing.T, c *http.Client, urlStr string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, urlStr, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("delete %s: %v", urlStr, err)
	}
	return resp
}

func mustDecode(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.NewDecoder(body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// requireCreateConversation POSTs /v1/conversations and returns the
// created conversation's id as a string. Asserts 201, decodes the
// JSON, and verifies a non-empty id — replaces the inline post +
// io.ReadAll + json.Unmarshal patterns CodeRabbit caught on PR #36.
func requireCreateConversation(t *testing.T, h *testutil.Harness, c *http.Client, body any) string {
	t.Helper()
	r := post(t, c, h.Server.URL+"/v1/conversations", body)
	rb, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("setup create conversation status=%d body=%s", r.StatusCode, rb)
	}
	var created map[string]any
	if err := json.Unmarshal(rb, &created); err != nil {
		t.Fatalf("setup create conversation: decode: %v\nbody=%s", err, rb)
	}
	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("setup create conversation: missing id: %s", rb)
	}
	return id
}

// --- POST /v1/conversations -------------------------------------------

func TestCreateConversation_Direct(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)

	resp := post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["type"] != "direct" {
		t.Errorf("type = %v, want direct", got["type"])
	}
	members, _ := got["members"].([]any)
	if len(members) != 2 {
		t.Errorf("members len = %d, want 2", len(members))
	}
}

func TestCreateConversation_DirectDeduplicates(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)

	r1 := post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	body1, _ := io.ReadAll(r1.Body)
	_ = r1.Body.Close()
	if r1.StatusCode != http.StatusCreated {
		t.Fatalf("first: status=%d body=%s", r1.StatusCode, body1)
	}
	var first map[string]any
	_ = json.Unmarshal(body1, &first)

	r2 := post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	t.Cleanup(func() { _ = r2.Body.Close() })
	body2, _ := io.ReadAll(r2.Body)
	if r2.StatusCode != http.StatusCreated {
		t.Fatalf("second: status=%d body=%s", r2.StatusCode, body2)
	}
	var second map[string]any
	_ = json.Unmarshal(body2, &second)
	if first["id"] != second["id"] {
		t.Errorf("dedupe failed: %v vs %v", first["id"], second["id"])
	}
}

func TestCreateConversation_Group(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	_, uc := h.AuthClient(t)

	resp := post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "group", "name": "Crew", "member_ids": []uuid.UUID{ub.ID, uc.ID},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["type"] != "group" {
		t.Errorf("type = %v, want group", got["type"])
	}
	if got["name"] != "Crew" {
		t.Errorf("name = %v, want Crew", got["name"])
	}
	members, _ := got["members"].([]any)
	if len(members) != 3 {
		t.Errorf("members len = %d, want 3", len(members))
	}
}

func TestCreateConversation_BadType(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	resp := post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "bogus", "member_ids": []uuid.UUID{ub.ID},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestCreateConversation_MissingTarget(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	resp := post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{uuid.New()},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestCreateConversation_GroupRequiresName(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	resp := post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "group", "member_ids": []uuid.UUID{ub.ID},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestCreateConversation_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{uuid.New()},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- GET /v1/conversations/{id} ---------------------------------------

func TestGetConversation_NonMemberSees404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)

	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})

	resp, err := stranger.Get(h.Server.URL + "/v1/conversations/" + id)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestGetConversation_BadUUID(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/conversations/not-a-uuid")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// --- GET /v1/conversations -------------------------------------------

func TestListConversations_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	_ = post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	}).Body.Close()

	resp, err := a.Get(h.Server.URL + "/v1/conversations")
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
	if len(data) != 1 {
		t.Errorf("len = %d, want 1", len(data))
	}
}

// TestListConversations_HidesDirectWithBlockedUser locks in the rule
// that a direct conversation with a blocked user is filtered out of
// /v1/conversations for the caller. Group conversations are unaffected
// even when one member is blocked — that rule has its own assertion
// below.
func TestListConversations_HidesDirectWithBlockedUser(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	// Pre-block: Alice creates the DM, then blocks Bob. The DM row
	// stays in the DB (history preserved); the LIST endpoint just
	// stops returning it for Alice.
	_ = requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{bob.ID},
	})
	if _, err := h.FriendRepo.Create(context.Background(), friendship.CreateParams{
		ID:          uuid.Must(uuid.NewV7()),
		RequesterID: alice.ID,
		AddresseeID: bob.ID,
		Status:      domain.FriendshipBlocked,
	}); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	resp, err := a.Get(h.Server.URL + "/v1/conversations")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("list status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	data, _ := got["data"].([]any)
	if len(data) != 0 {
		t.Errorf("blocked DM still listed: len=%d", len(data))
	}
}

// TestListConversations_GroupWithBlockedMemberStaysVisible documents
// the deliberate split: blocking a friend hides their DM, but a group
// you're both in stays visible (Phase 6's thread render hides the
// blocked sender's bubbles per-message). The whole-group filter
// would lock people out of work / family chats just because one
// member was blocked, which isn't the intent.
func TestListConversations_GroupWithBlockedMemberStaysVisible(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	_, carol := h.AuthClient(t)
	groupName := "Roommates"
	_ = requireCreateConversation(t, h, a, map[string]any{
		"type":       "group",
		"name":       groupName,
		"member_ids": []uuid.UUID{bob.ID, carol.ID},
	})
	if _, err := h.FriendRepo.Create(context.Background(), friendship.CreateParams{
		ID:          uuid.Must(uuid.NewV7()),
		RequesterID: alice.ID,
		AddresseeID: bob.ID,
		Status:      domain.FriendshipBlocked,
	}); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	resp, err := a.Get(h.Server.URL + "/v1/conversations")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("list status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	data, _ := got["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("group not visible after blocking a member: len=%d", len(data))
	}
	row, _ := data[0].(map[string]any)
	if row["name"] != groupName {
		t.Errorf("name = %v, want %q", row["name"], groupName)
	}
}

// --- PATCH /v1/conversations/{id} -------------------------------------

func TestUpdateConversation_AdminRenames(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)

	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "group", "name": "Old", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := patchJSON(t, a, h.Server.URL+"/v1/conversations/"+id, map[string]any{"name": "New"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
	got := mustDecode(t, resp.Body)
	if got["name"] != "New" {
		t.Errorf("name = %v, want New", got["name"])
	}
}

func TestUpdateConversation_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	bClient, ub := h.AuthClient(t)

	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "group", "name": "Old", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := patchJSON(t, bClient, h.Server.URL+"/v1/conversations/"+id, map[string]any{"name": "New"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusForbidden, apierror.CodeForbidden)
}

func TestUpdateConversation_NonMemberSees404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)

	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "group", "name": "Old", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := patchJSON(t, stranger, h.Server.URL+"/v1/conversations/"+id, map[string]any{"name": "X"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

// --- DELETE /v1/conversations/{id} (Leave) ---------------------------

func TestLeaveConversation_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)

	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := deleteReqHTTP(t, a, h.Server.URL+"/v1/conversations/"+id)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}

	// a's GET is now 404; b's still 200 (the other party still sees it).
	r2, _ := a.Get(h.Server.URL + "/v1/conversations/" + id)
	t.Cleanup(func() { _ = r2.Body.Close() })
	if r2.StatusCode != http.StatusNotFound {
		t.Errorf("post-leave GET status=%d, want 404", r2.StatusCode)
	}
}

// --- POST /v1/conversations/{id}/members + DELETE -----------------

func TestAddMembers_AdminAdds(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	_, uc := h.AuthClient(t)

	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "group", "name": "Crew", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := post(t, a, h.Server.URL+"/v1/conversations/"+id+"/members", map[string]any{
		"user_ids": []uuid.UUID{uc.ID},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
	got := mustDecode(t, resp.Body)
	added, _ := got["added"].([]any)
	if len(added) != 1 {
		t.Errorf("added len = %d, want 1", len(added))
	}
}

func TestAddMembers_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	bClient, ub := h.AuthClient(t)
	_, uc := h.AuthClient(t)

	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "group", "name": "Crew", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := post(t, bClient, h.Server.URL+"/v1/conversations/"+id+"/members", map[string]any{
		"user_ids": []uuid.UUID{uc.ID},
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusForbidden, apierror.CodeForbidden)
}

func TestRemoveMember_AdminKicks(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)

	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "group", "name": "Crew", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := deleteReqHTTP(t, a, h.Server.URL+"/v1/conversations/"+id+"/members/"+ub.ID.String())
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
}

// --- POST /v1/conversations/{id}/read --------------------------------
//
// MarkRead success requires a real message row because
// `last_read_message_id` has a FK to messages (added in migration 0005).
// The full happy path is exercised in Phase 6 once the message repo
// lands and the conversation smoke test (§16 milestone 5.4) drives
// MarkRead with a real id. Here we only cover the auth/membership
// boundaries that don't depend on writing a real message.

func TestMarkRead_NonMemberSees404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	id := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := post(t, stranger, h.Server.URL+"/v1/conversations/"+id+"/read", map[string]any{
		"up_to_message_id": uuid.Must(uuid.NewV7()),
	})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

// --- DTO no-leak ------------------------------------------------------

func TestConversationDTOs_NoLeak(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	r := post(t, a, h.Server.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	t.Cleanup(func() { _ = r.Body.Close() })
	body, _ := io.ReadAll(r.Body)
	for _, leak := range []string{"password_hash", "PasswordHash", "deleted_at", "email"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("response leaked %q: %s", leak, body)
		}
	}
}

// silence unused if domain ever drops the helper.
var _ = domain.ConversationGroup
