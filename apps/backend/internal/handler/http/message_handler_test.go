package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// requireSendMessage POSTs a message and returns the created message id
// as a string. Asserts 201 + decodes the JSON.
func requireSendMessage(t *testing.T, h *testutil.Harness, c *http.Client, convID, body string) string {
	t.Helper()
	r := post(t, c, h.Server.URL+"/v1/conversations/"+convID+"/messages",
		map[string]any{"body": body})
	rb, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("setup send message status=%d body=%s", r.StatusCode, rb)
	}
	var created map[string]any
	if err := json.Unmarshal(rb, &created); err != nil {
		t.Fatalf("setup send message: decode: %v\nbody=%s", err, rb)
	}
	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("setup send message: missing id: %s", rb)
	}
	return id
}

// --- POST /v1/conversations/{id}/messages ---------------------------

func TestSendMessage_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})

	resp := post(t, a, h.Server.URL+"/v1/conversations/"+cid+"/messages",
		map[string]any{"body": "hello world"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["body"] != "hello world" {
		t.Errorf("body = %v, want hello world", got["body"])
	}
	if got["is_deleted"].(bool) {
		t.Errorf("is_deleted = true, want false")
	}
}

func TestSendMessage_NonMemberSees404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	resp := post(t, stranger, h.Server.URL+"/v1/conversations/"+cid+"/messages",
		map[string]any{"body": "i shouldn't be here"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestSendMessage_EmptyBody(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	resp := post(t, a, h.Server.URL+"/v1/conversations/"+cid+"/messages",
		map[string]any{"body": "   "})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestSendMessage_BadConvUUID(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	resp := post(t, a, h.Server.URL+"/v1/conversations/not-a-uuid/messages",
		map[string]any{"body": "x"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

func TestSendMessage_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/conversations/"+uuid.New().String()+"/messages",
		map[string]any{"body": "x"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- GET /v1/conversations/{id}/messages ----------------------------

func TestListMessages_Success(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	for i := 0; i < 3; i++ {
		_ = requireSendMessage(t, h, a, cid, "msg")
	}

	resp, err := a.Get(h.Server.URL + "/v1/conversations/" + cid + "/messages")
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
	if len(data) != 3 {
		t.Errorf("len = %d, want 3", len(data))
	}
}

func TestListMessages_Query(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	_ = requireSendMessage(t, h, a, cid, "the quick brown fox")
	_ = requireSendMessage(t, h, a, cid, "lazy dog rests")

	resp, err := a.Get(h.Server.URL + "/v1/conversations/" + cid + "/messages?q=fox")
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
		t.Errorf("len = %d, want 1 match", len(data))
	}
}

func TestListMessages_NonMember(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	resp, err := stranger.Get(h.Server.URL + "/v1/conversations/" + cid + "/messages")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

// --- PATCH /v1/messages/{id} ----------------------------------------

func TestEditMessage_Owner(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	mid := requireSendMessage(t, h, a, cid, "first")

	resp := patchJSON(t, a, h.Server.URL+"/v1/messages/"+mid, map[string]any{"body": "second"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["body"] != "second" {
		t.Errorf("body = %v, want second", got["body"])
	}
	if got["edited_at"] == nil {
		t.Errorf("edited_at should be set")
	}
}

func TestEditMessage_NonOwnerForbidden(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, b := h.AuthClient(t)
	bClient, _ := h.AuthClient(t)
	_ = b
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	mid := requireSendMessage(t, h, a, cid, "mine")

	resp := patchJSON(t, bClient, h.Server.URL+"/v1/messages/"+mid, map[string]any{"body": "hacked"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusForbidden, apierror.CodeForbidden)
}

func TestEditMessage_NotFound(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	resp := patchJSON(t, a, h.Server.URL+"/v1/messages/"+uuid.New().String(),
		map[string]any{"body": "x"})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestEditMessage_OverlongBody(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	mid := requireSendMessage(t, h, a, cid, "ok")
	resp := patchJSON(t, a, h.Server.URL+"/v1/messages/"+mid,
		map[string]any{"body": strings.Repeat("x", 10001)})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

// --- DELETE /v1/messages/{id} ---------------------------------------

func TestDeleteMessage_OwnerSucceeds(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	mid := requireSendMessage(t, h, a, cid, "delete me")

	resp := deleteReqHTTP(t, a, h.Server.URL+"/v1/messages/"+mid)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	// Subsequent List should still surface the row, but with body blanked
	// and is_deleted=true (§4.6 placeholder rendering).
	listResp, err := a.Get(h.Server.URL + "/v1/conversations/" + cid + "/messages")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	t.Cleanup(func() { _ = listResp.Body.Close() })
	got := mustDecode(t, listResp.Body)
	data, _ := got["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("len = %d, want 1", len(data))
	}
	row := data[0].(map[string]any)
	if row["body"] != "" {
		t.Errorf("body should be blanked, got %q", row["body"])
	}
	if !row["is_deleted"].(bool) {
		t.Errorf("is_deleted should be true")
	}
}

func TestDeleteMessage_StrangerForbidden(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	mid := requireSendMessage(t, h, a, cid, "private")

	resp := deleteReqHTTP(t, stranger, h.Server.URL+"/v1/messages/"+mid)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusForbidden, apierror.CodeForbidden)
}

func TestDeleteMessage_Idempotent(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	mid := requireSendMessage(t, h, a, cid, "twice")
	r1 := deleteReqHTTP(t, a, h.Server.URL+"/v1/messages/"+mid)
	t.Cleanup(func() { _ = r1.Body.Close() })
	if r1.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(r1.Body)
		t.Fatalf("first delete status=%d body=%s", r1.StatusCode, body)
	}
	r2 := deleteReqHTTP(t, a, h.Server.URL+"/v1/messages/"+mid)
	t.Cleanup(func() { _ = r2.Body.Close() })
	if r2.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(r2.Body)
		t.Fatalf("second delete status=%d body=%s", r2.StatusCode, body)
	}
}

// --- GET /v1/messages/{id}/reads ------------------------------------

func TestListReads_NonMemberSees404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	mid := requireSendMessage(t, h, a, cid, "secret")

	resp, err := stranger.Get(h.Server.URL + "/v1/messages/" + mid + "/reads")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestListReads_MemberEmpty(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, ub := h.AuthClient(t)
	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{ub.ID},
	})
	mid := requireSendMessage(t, h, a, cid, "no readers yet")

	resp, err := a.Get(h.Server.URL + "/v1/messages/" + mid + "/reads")
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
	if len(data) != 0 {
		t.Errorf("len = %d, want 0", len(data))
	}
}
