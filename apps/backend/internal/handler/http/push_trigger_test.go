package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// §11.5 wires push notifications into the message / friend / call paths.
// These tests exercise the trigger sites end-to-end via the harness so
// the wiring (handler → service → notification → pushnotif) is checked
// in one place. The recipient is registered with a device token but
// never connects a WS — the presence repo returns "offline" for them,
// which is exactly the gate the push fan-out uses.

// --- POST /v1/conversations/{id}/messages -------------------------------

func TestSendMessage_FiresOfflinePush_Direct(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	bClient, b := h.AuthClient(t)

	if _, err := h.DeviceRepo.Register(context.Background(), b.ID, "ExponentPushToken[b]", domain.DeviceIOS); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	// b registered but never hit the WS — presence service returns
	// "offline" for them → push should fire.
	_ = bClient

	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{b.ID},
	})
	requireSendMessage(t, h, a, cid, "hi from a")

	pushes := h.Pusher.Snapshot()
	if len(pushes) != 1 {
		t.Fatalf("expected 1 push for offline recipient, got %d: %+v", len(pushes), pushes)
	}
	got := pushes[0]
	if len(got.Tokens) != 1 || got.Tokens[0] != "ExponentPushToken[b]" {
		t.Errorf("unexpected tokens: %+v", got.Tokens)
	}
	if got.Data["type"] != "message" {
		t.Errorf("data.type = %v, want message", got.Data["type"])
	}
	if got.Data["conversation_id"] != cid {
		t.Errorf("data.conversation_id = %v, want %v", got.Data["conversation_id"], cid)
	}
}

func TestSendMessage_DoesNotPushSender(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, ua := h.AuthClient(t)
	_, b := h.AuthClient(t)
	// Register a device for the sender. They must NOT receive their own message push.
	if _, err := h.DeviceRepo.Register(context.Background(), ua.ID, "ExponentPushToken[a]", domain.DeviceIOS); err != nil {
		t.Fatalf("seed device a: %v", err)
	}

	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{b.ID},
	})
	requireSendMessage(t, h, a, cid, "self push?")

	for _, p := range h.Pusher.Snapshot() {
		for _, tok := range p.Tokens {
			if tok == "ExponentPushToken[a]" {
				t.Errorf("sender should not receive offline push; got %+v", p)
			}
		}
	}
}

func TestSendMessage_NoPushForOnlineRecipient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, b := h.AuthClient(t)

	if _, err := h.DeviceRepo.Register(ctx, b.ID, "ExponentPushToken[b]", domain.DeviceIOS); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	// Mark b as actively online via the presence service so the push
	// fan-out treats them as "live WS" and skips them.
	if err := h.PresenceSvc.SetStatus(ctx, b.ID, domain.PresenceOnline.Ptr()); err != nil {
		t.Fatalf("set b online: %v", err)
	}

	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{b.ID},
	})
	requireSendMessage(t, h, a, cid, "online recipient")

	if pushes := h.Pusher.Snapshot(); len(pushes) != 0 {
		t.Errorf("expected 0 pushes for online recipient, got %d: %+v", len(pushes), pushes)
	}
}

// --- POST /v1/friends/requests ------------------------------------------

func TestSendFriendRequest_FiresOfflinePush(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, b := h.AuthClient(t)

	if _, err := h.DeviceRepo.Register(context.Background(), b.ID, "ExponentPushToken[b]", domain.DeviceIOS); err != nil {
		t.Fatalf("seed device: %v", err)
	}

	resp := post(t, a, h.Server.URL+"/v1/friends/requests",
		map[string]any{"username": b.Username})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	pushes := h.Pusher.Snapshot()
	if len(pushes) != 1 {
		t.Fatalf("expected 1 friend-request push, got %d: %+v", len(pushes), pushes)
	}
	got := pushes[0]
	if got.Data["type"] != "friend_request" {
		t.Errorf("data.type = %v, want friend_request", got.Data["type"])
	}
	if got.Title != "Friend request" {
		t.Errorf("title = %q, want Friend request", got.Title)
	}
}

func TestSendFriendRequest_NoPushWhenAddresseeOnline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, b := h.AuthClient(t)

	if _, err := h.DeviceRepo.Register(ctx, b.ID, "ExponentPushToken[b]", domain.DeviceIOS); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if err := h.PresenceSvc.SetStatus(ctx, b.ID, domain.PresenceOnline.Ptr()); err != nil {
		t.Fatalf("set b online: %v", err)
	}

	resp := post(t, a, h.Server.URL+"/v1/friends/requests",
		map[string]any{"username": b.Username})
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	if pushes := h.Pusher.Snapshot(); len(pushes) != 0 {
		t.Errorf("expected 0 pushes for online addressee, got %d: %+v", len(pushes), pushes)
	}
}

// --- pref-off short-circuit ---------------------------------------------

// Even with an offline recipient + a device token, if the user has
// toggled the relevant category off, no push should fire. This is the
// notificationpref.ShouldNotify gate inside notification.SendOfflinePush
// — we exercise it from end-to-end here so the wiring is checked.
func TestSendMessage_PrefOffSuppressesPush(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := testutil.New(t)
	a, _ := h.AuthClient(t)
	_, b := h.AuthClient(t)

	if _, err := h.DeviceRepo.Register(ctx, b.ID, "ExponentPushToken[b]", domain.DeviceIOS); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	off := false
	if _, err := h.NotifPrefSvc.UpdateForUser(ctx, notificationpref.UpdateParams{
		UserID:         b.ID,
		DirectMessages: &off,
	}); err != nil {
		t.Fatalf("set pref off: %v", err)
	}

	cid := requireCreateConversation(t, h, a, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{b.ID},
	})
	requireSendMessage(t, h, a, cid, "should not push")

	if pushes := h.Pusher.Snapshot(); len(pushes) != 0 {
		t.Errorf("pref off → expected 0 pushes, got %d: %+v", len(pushes), pushes)
	}
}
