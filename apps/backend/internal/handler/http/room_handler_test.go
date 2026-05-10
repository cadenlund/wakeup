package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeDirectViaService creates an alice<->bob direct via the conv
// service so the harness's room handler has a real conversation to
// authorize against. Establishes the friendship first because the
// service rejects direct creates between non-friends.
func makeDirectViaService(t *testing.T, h *testutil.Harness, a, b domain.User) string {
	t.Helper()
	h.MakeFriendship(t, a, b)
	res, err := h.ConvSvc.Create(context.Background(), conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	if err != nil {
		t.Fatalf("Create direct: %v", err)
	}
	return res.Conversation.ID.String()
}

// --- POST /room/join --------------------------------------------------

func TestJoinRoom_MemberSucceeds(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	cid := makeDirectViaService(t, h, alice, bob)

	resp := post(t, c, h.Server.URL+"/v1/conversations/"+cid+"/room/join",
		map[string]any{"video": false})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	for _, key := range []string{"room_id", "livekit_url", "livekit_token", "expires_at", "video"} {
		if _, ok := got[key]; !ok {
			t.Errorf("response missing %q", key)
		}
	}
	if room, _ := got["room_id"].(string); !strings.HasPrefix(room, "conv:"+cid) {
		t.Errorf("room_id = %v, want prefix conv:%s", got["room_id"], cid)
	}
}

// Empty body is valid — `video` defaults to false.
func TestJoinRoom_EmptyBodySucceeds(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	cid := makeDirectViaService(t, h, alice, bob)

	req, _ := http.NewRequest(http.MethodPost, h.Server.URL+"/v1/conversations/"+cid+"/room/join", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestJoinRoom_NonMemberSees404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	_, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	cid := makeDirectViaService(t, h, alice, bob)

	resp := post(t, stranger, h.Server.URL+"/v1/conversations/"+cid+"/room/join", map[string]any{})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestJoinRoom_BadUUID(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp := post(t, c, h.Server.URL+"/v1/conversations/not-a-uuid/room/join", map[string]any{})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

func TestJoinRoom_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := post(t, c, h.Server.URL+"/v1/conversations/"+uuid.New().String()+"/room/join", map[string]any{})
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- POST /room/leave -------------------------------------------------

func TestLeaveRoom_MemberSucceeds(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	cid := makeDirectViaService(t, h, alice, bob)

	resp := post(t, c, h.Server.URL+"/v1/conversations/"+cid+"/room/leave", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestLeaveRoom_NonMemberSees404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	_, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	cid := makeDirectViaService(t, h, alice, bob)
	resp := post(t, stranger, h.Server.URL+"/v1/conversations/"+cid+"/room/leave", nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

// --- GET /room --------------------------------------------------------

func TestGetRoomState_EmptyForFreshConversation(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	cid := makeDirectViaService(t, h, alice, bob)

	resp, err := c.Get(h.Server.URL + "/v1/conversations/" + cid + "/room")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	parts, _ := got["participants"].([]any)
	if len(parts) != 0 {
		t.Errorf("participants len = %d, want 0", len(parts))
	}
	if got["started_at"] != nil {
		t.Errorf("started_at = %v, want nil", got["started_at"])
	}
}

// After a webhook-side AddParticipant + MarkStarted, the GET endpoint
// surfaces the participant + started_at.
func TestGetRoomState_AfterParticipantAdded(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	cid := makeDirectViaService(t, h, alice, bob)
	convID := uuid.MustParse(cid)

	if _, err := h.RoomSvc.AddParticipant(context.Background(), convID, alice.ID); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}
	if _, err := h.RoomSvc.MarkStarted(context.Background(), convID); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}

	resp, err := c.Get(h.Server.URL + "/v1/conversations/" + cid + "/room")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	parts, _ := got["participants"].([]any)
	if len(parts) != 1 {
		t.Fatalf("participants len = %d, want 1", len(parts))
	}
	if got["started_at"] == nil {
		t.Errorf("started_at is nil after MarkStarted")
	}
}

func TestGetRoomState_NonMemberSees404(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	_, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	cid := makeDirectViaService(t, h, alice, bob)
	resp, err := stranger.Get(h.Server.URL + "/v1/conversations/" + cid + "/room")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}
