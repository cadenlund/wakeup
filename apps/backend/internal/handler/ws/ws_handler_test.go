package ws_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/handler/ws"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// dialWS opens a WebSocket against the harness's TLS test server using
// the given http.Client (which carries the auth cookies). The harness
// listener is HTTPS, so the WS URL is wss://. The client's Transport
// already trusts the test cert.
func dialWS(t *testing.T, c *http.Client, urlStr string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(urlStr, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tr, _ := c.Transport.(*http.Transport)
	if tr == nil {
		tr = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	}
	dialClient := &http.Client{Transport: tr, Jar: c.Jar}
	return websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: dialClient,
	})
}

// --- Auth gating ------------------------------------------------------

func TestWSHandler_UnauthenticatedRejected(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)

	conn, resp, err := dialWS(t, c, h.Server.URL+"/v1/ws")
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected dial to fail without session")
	}
	if resp == nil {
		t.Fatalf("dial err = %v but no response (want 401)", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestWSHandler_AuthenticatedDialSucceeds(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, u := h.AuthClient(t)

	conn, resp, err := dialWS(t, c, h.Server.URL+"/v1/ws")
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	// Hub should now show 1 conn for this user.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.WSHub.ConnCount(u.ID) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("hub has %d conns for user, want 1", h.WSHub.ConnCount(u.ID))
}

// --- Inbound event routing -------------------------------------------

func TestWSHandler_HeartbeatIsNoOp(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	conn, resp, err := dialWS(t, c, h.Server.URL+"/v1/ws")
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	// Send a heartbeat envelope. The handler must accept it without
	// closing the connection, but no S→C frame is expected back.
	payload, err := wsproto.Encode(wsproto.EventHeartbeat, wsproto.HeartbeatPayload{})
	if err != nil {
		t.Fatalf("Encode heartbeat: %v", err)
	}
	if err := conn.Write(context.Background(), websocket.MessageText, payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Briefly verify no echo frame arrives.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, frame, err := conn.Read(ctx)
	if err == nil {
		t.Errorf("got unexpected frame after heartbeat: %s", frame)
	}
}

// Typing.start: the handler should re-publish on the conv channel with
// the server-stamped user_id, fanning to other members of the conv.
func TestWSHandler_TypingFansOutToOtherConvMembers(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	aliceClient, alice := h.AuthClient(t)
	bobClient, bob := h.AuthClient(t)

	// Create a direct between alice and bob via the conversation
	// service so both have the channel subscription wired on dial.
	res, err := h.ConvSvc.Create(context.Background(), convsvc.CreateParams{
		Type: domain.ConversationDirect, Creator: alice.ID, MemberIDs: []uuid.UUID{bob.ID},
	})
	if err != nil {
		t.Fatalf("Create direct: %v", err)
	}
	convID := res.Conversation.ID

	// Dial both clients; bob's channels include conv:<id>:messages.
	aliceConn, aResp, err := dialWS(t, aliceClient, h.Server.URL+"/v1/ws")
	if aResp != nil && aResp.Body != nil {
		_ = aResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("alice dial: %v", err)
	}
	t.Cleanup(func() { _ = aliceConn.Close(websocket.StatusNormalClosure, "") })

	bobConn, bResp, err := dialWS(t, bobClient, h.Server.URL+"/v1/ws")
	if bResp != nil && bResp.Body != nil {
		_ = bResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("bob dial: %v", err)
	}
	t.Cleanup(func() { _ = bobConn.Close(websocket.StatusNormalClosure, "") })

	// Wait for both registrations. Fail-fast on timeout — letting the
	// test continue with one or zero registered conns would surface
	// later as a misleading "Read timed out" instead of "registration
	// never landed". (CodeRabbit PR #49.)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.WSHub.ConnCount(alice.ID) == 1 && h.WSHub.ConnCount(bob.ID) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if gotA, gotB := h.WSHub.ConnCount(alice.ID), h.WSHub.ConnCount(bob.ID); gotA != 1 || gotB != 1 {
		t.Fatalf("hub registration not ready: alice=%d bob=%d, want 1 and 1", gotA, gotB)
	}

	// Alice sends typing.start. Bob must receive a typing.start with
	// alice's user_id stamped server-side.
	typing, err := wsproto.Encode(wsproto.EventTypingStart, wsproto.TypingPayload{
		ConversationID: convID,
	})
	if err != nil {
		t.Fatalf("Encode typing: %v", err)
	}
	if err := aliceConn.Write(context.Background(), websocket.MessageText, typing); err != nil {
		t.Fatalf("alice Write: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, raw, err := bobConn.Read(ctx)
	if err != nil {
		t.Fatalf("bob Read: %v", err)
	}
	var got wsproto.Envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("bob decode: %v", err)
	}
	if got.Type != wsproto.EventTypingStart {
		t.Errorf("type = %s, want %s", got.Type, wsproto.EventTypingStart)
	}
	var payload wsproto.TypingPayload
	if err := wsproto.UnmarshalData(got, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.ConversationID != convID {
		t.Errorf("conversation_id = %v, want %v", payload.ConversationID, convID)
	}
	if payload.UserID == nil || *payload.UserID != alice.ID {
		t.Errorf("user_id = %v, want %v (server-stamped)", payload.UserID, alice.ID)
	}
}

// Membership gate on typing: a client cannot publish typing events
// on a conversation they're not a member of. The handler must reject
// without re-publishing, even if the client knows the conv_id from a
// prior interaction. (CodeRabbit PR #49.)
func TestWSHandler_TypingRejectsNonMember(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	aliceClient, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)
	strangerClient, _ := h.AuthClient(t)

	// alice + bob make a direct; stranger isn't a member.
	res, err := h.ConvSvc.Create(context.Background(), convsvc.CreateParams{
		Type: domain.ConversationDirect, Creator: alice.ID, MemberIDs: []uuid.UUID{bob.ID},
	})
	if err != nil {
		t.Fatalf("Create direct: %v", err)
	}
	convID := res.Conversation.ID

	// Alice connects so she would receive a real typing.start broadcast
	// if the stranger's send had been honored.
	aliceConn, aResp, err := dialWS(t, aliceClient, h.Server.URL+"/v1/ws")
	if aResp != nil && aResp.Body != nil {
		_ = aResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("alice dial: %v", err)
	}
	t.Cleanup(func() { _ = aliceConn.Close(websocket.StatusNormalClosure, "") })

	// Stranger connects + tries to type into alice/bob's direct.
	strangerConn, sResp, err := dialWS(t, strangerClient, h.Server.URL+"/v1/ws")
	if sResp != nil && sResp.Body != nil {
		_ = sResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("stranger dial: %v", err)
	}
	t.Cleanup(func() { _ = strangerConn.Close(websocket.StatusNormalClosure, "") })

	typing, err := wsproto.Encode(wsproto.EventTypingStart, wsproto.TypingPayload{
		ConversationID: convID,
	})
	if err != nil {
		t.Fatalf("Encode typing: %v", err)
	}
	if err := strangerConn.Write(context.Background(), websocket.MessageText, typing); err != nil {
		t.Fatalf("stranger Write: %v", err)
	}

	// Alice must NOT receive a typing event.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, frame, err := aliceConn.Read(ctx)
	if err == nil {
		t.Errorf("alice received a typing event from a non-member: %s", frame)
	}
}

// Disconnect cleanup: when a client closes the conn, the hub clears
// the registration AND the bridge clears every channel subscription
// for that user.
func TestWSHandler_DisconnectClearsBridgeSubscriptions(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	aliceClient, alice := h.AuthClient(t)
	_, bob := h.AuthClient(t)

	// Give alice at least one conv so the bridge has something to
	// subscribe to.
	res, err := h.ConvSvc.Create(context.Background(), convsvc.CreateParams{
		Type: domain.ConversationDirect, Creator: alice.ID, MemberIDs: []uuid.UUID{bob.ID},
	})
	if err != nil {
		t.Fatalf("Create direct: %v", err)
	}
	_ = res

	conn, resp, err := dialWS(t, aliceClient, h.Server.URL+"/v1/ws")
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Wait until registered AND the bridge has subscribed her to her
	// conversation channels (user:<id>:events + at least one conv:*).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.WSHub.ConnCount(alice.ID) == 1 && h.WSBridge.SubscriptionCount(alice.ID) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := h.WSBridge.SubscriptionCount(alice.ID); got < 2 {
		t.Fatalf("pre-close SubscriptionCount = %d, want >= 2 (user channel + conv channel)", got)
	}

	// Close the client side. Server-side Run unwinds and the deferred
	// bridge.UnsubscribeAll runs.
	_ = conn.Close(websocket.StatusNormalClosure, "bye")

	// Hub count drops to 0 AND bridge subscription count drops to 0
	// — the latter is the real assertion CodeRabbit asked for: stale
	// pubsub interest left behind a closed conn would hold the broker
	// keyspace open for a ghost subscriber.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.WSHub.ConnCount(alice.ID) == 0 && h.WSBridge.SubscriptionCount(alice.ID) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := h.WSHub.ConnCount(alice.ID); got != 0 {
		t.Errorf("ConnCount after disconnect = %d, want 0", got)
	}
	if got := h.WSBridge.SubscriptionCount(alice.ID); got != 0 {
		t.Errorf("SubscriptionCount after disconnect = %d, want 0", got)
	}
}

// Config validation — at least one constructor-rejection.
func TestNewHandler_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := ws.NewHandler(ws.HandlerConfig{}); err == nil {
		t.Error("nil deps should error")
	}
}
