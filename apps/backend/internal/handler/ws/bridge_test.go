package ws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/handler/ws"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
)

// bridgeHarness wires Hub + Bridge + InProc broker + a tiny upgrade
// handler so tests can assert end-to-end pubsub → WS fan-out.
type bridgeHarness struct {
	hub    *ws.Hub
	bridge *ws.Bridge
	broker pubsub.Broker
	server *httptest.Server
	url    string
	userID uuid.UUID
}

func newBridgeHarness(t *testing.T, userID uuid.UUID) *bridgeHarness {
	t.Helper()
	hub := ws.NewHub(nil)
	broker := pubsub.NewInProc(pubsub.NewRegistry())
	t.Cleanup(func() { _ = broker.Close() })

	bridge, err := ws.NewBridge(hub, broker, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	t.Cleanup(bridge.Close)

	bh := &bridgeHarness{hub: hub, bridge: bridge, broker: broker, userID: userID}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, ws.AcceptOptions([]string{"*"}))
		if err != nil {
			return
		}
		conn, err := ws.NewConn(ws.ConnConfig{UserID: userID, WS: c, Hub: hub})
		if err != nil {
			_ = c.Close(websocket.StatusInternalError, "newconn")
			return
		}
		hub.Register(conn)
		_ = conn.Run(r.Context())
	})
	bh.server = httptest.NewServer(mux)
	t.Cleanup(bh.server.Close)
	bh.url = "ws" + bh.server.URL[len("http"):] + "/ws"
	return bh
}

// --- Subscribe + dispatch --------------------------------------------

func TestBridge_PublishedEventReachesSubscribedUser(t *testing.T) {
	t.Parallel()
	uid := uuid.Must(uuid.NewV7())
	bh := newBridgeHarness(t, uid)

	// Connect and subscribe BEFORE publishing.
	c := dial(t, bh.url)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	waitConnCount(t, bh.hub, uid, 1)

	channel := "conv:" + uuid.Must(uuid.NewV7()).String() + ":messages"
	if err := bh.bridge.Subscribe(context.Background(), uid, channel); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bh.broker.Publish(context.Background(), channel, []byte(`{"type":"message.new","data":{"hi":1}}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := readWithTimeout(t, c, 2*time.Second)
	if string(got) == "" {
		t.Errorf("got empty payload")
	}
}

// Cross-channel isolation: a publish on a channel only reaches users
// who subscribed to it.
func TestBridge_NoLeakAcrossChannels(t *testing.T) {
	t.Parallel()
	uid := uuid.Must(uuid.NewV7())
	bh := newBridgeHarness(t, uid)
	c := dial(t, bh.url)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	waitConnCount(t, bh.hub, uid, 1)

	subbed := "conv:1:messages"
	other := "conv:2:messages"

	if err := bh.bridge.Subscribe(context.Background(), uid, subbed); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// Publish on the channel the user did NOT subscribe to.
	if err := bh.broker.Publish(context.Background(), other, []byte("{}")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, frame, err := c.Read(ctx)
	if err == nil {
		t.Errorf("got frame %q on channel %q the user is not subscribed to", frame, other)
	}
}

// Unsubscribe stops delivery for that channel.
func TestBridge_UnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()
	uid := uuid.Must(uuid.NewV7())
	bh := newBridgeHarness(t, uid)
	c := dial(t, bh.url)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	waitConnCount(t, bh.hub, uid, 1)

	channel := "conv:dropme:messages"
	if err := bh.bridge.Subscribe(context.Background(), uid, channel); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// Unsubscribe immediately, then publish — nothing should arrive.
	if err := bh.bridge.Unsubscribe(context.Background(), uid, channel); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if err := bh.broker.Publish(context.Background(), channel, []byte("{}")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, frame, err := c.Read(ctx)
	if err == nil {
		t.Errorf("got frame %q after Unsubscribe", frame)
	}
}

// Multi-user fan-out: two users both subscribed to the same channel
// each receive the event.
func TestBridge_FansOutToEverySubscriber(t *testing.T) {
	t.Parallel()
	uidA := uuid.Must(uuid.NewV7())
	uidB := uuid.Must(uuid.NewV7())

	hub := ws.NewHub(nil)
	broker := pubsub.NewInProc(pubsub.NewRegistry())
	t.Cleanup(func() { _ = broker.Close() })
	bridge, err := ws.NewBridge(hub, broker, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	t.Cleanup(bridge.Close)

	// One server that lets the test pick the userID per request via a
	// "x-user" query param. Keeps the test concise.
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		uid, err := uuid.Parse(r.URL.Query().Get("uid"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c, err := websocket.Accept(w, r, ws.AcceptOptions([]string{"*"}))
		if err != nil {
			return
		}
		conn, err := ws.NewConn(ws.ConnConfig{UserID: uid, WS: c, Hub: hub})
		if err != nil {
			_ = c.Close(websocket.StatusInternalError, "newconn")
			return
		}
		hub.Register(conn)
		_ = conn.Run(r.Context())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	wsURL := "ws" + srv.URL[len("http"):] + "/ws"

	cA := dial(t, wsURL+"?uid="+uidA.String())
	cB := dial(t, wsURL+"?uid="+uidB.String())
	t.Cleanup(func() {
		_ = cA.Close(websocket.StatusNormalClosure, "")
		_ = cB.Close(websocket.StatusNormalClosure, "")
	})
	waitConnCount(t, hub, uidA, 1)
	waitConnCount(t, hub, uidB, 1)

	channel := "conv:shared:messages"
	if err := bridge.Subscribe(context.Background(), uidA, channel); err != nil {
		t.Fatalf("Subscribe A: %v", err)
	}
	if err := bridge.Subscribe(context.Background(), uidB, channel); err != nil {
		t.Fatalf("Subscribe B: %v", err)
	}

	if err := broker.Publish(context.Background(), channel, []byte(`{"type":"message.new"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	gotA := readWithTimeout(t, cA, 2*time.Second)
	gotB := readWithTimeout(t, cB, 2*time.Second)
	if string(gotA) != string(gotB) {
		t.Errorf("A vs B differ: %q vs %q", gotA, gotB)
	}
}

// UnsubscribeAll: clears every channel for one user. Used by the
// upgrade handler when a connection closes.
func TestBridge_UnsubscribeAllClearsEveryChannel(t *testing.T) {
	t.Parallel()
	uid := uuid.Must(uuid.NewV7())
	bh := newBridgeHarness(t, uid)
	c := dial(t, bh.url)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	waitConnCount(t, bh.hub, uid, 1)

	channels := []string{"conv:a:messages", "conv:b:messages", "conv:c:messages"}
	if err := bh.bridge.Subscribe(context.Background(), uid, channels...); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	emptied := bh.bridge.UnsubscribeAll(context.Background(), uid)
	if len(emptied) != len(channels) {
		t.Errorf("UnsubscribeAll cleared %d channels, want %d", len(emptied), len(channels))
	}

	// After UnsubscribeAll, a publish on any of those channels should
	// not reach the user.
	if err := bh.broker.Publish(context.Background(), channels[0], []byte("{}")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, frame, err := c.Read(ctx)
	if err == nil {
		t.Errorf("got frame %q after UnsubscribeAll", frame)
	}
}

// Subscribing twice on the same (user, channel) is idempotent — only
// one fan-out per event.
func TestBridge_SubscribeIsIdempotent(t *testing.T) {
	t.Parallel()
	uid := uuid.Must(uuid.NewV7())
	bh := newBridgeHarness(t, uid)
	c := dial(t, bh.url)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	waitConnCount(t, bh.hub, uid, 1)

	channel := "conv:dup:messages"
	for i := 0; i < 3; i++ {
		if err := bh.bridge.Subscribe(context.Background(), uid, channel); err != nil {
			t.Fatalf("Subscribe %d: %v", i, err)
		}
	}
	if err := bh.broker.Publish(context.Background(), channel, []byte("{}")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// First read succeeds with the published frame.
	_ = readWithTimeout(t, c, 2*time.Second)
	// A second read should NOT see a duplicate. Allow 200ms for any
	// trailing dispatcher work.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, frame, err := c.Read(ctx)
	if err == nil {
		t.Errorf("got duplicate frame %q after multiple Subscribe calls", frame)
	}
}

// --- Config validation ------------------------------------------------

func TestNewBridge_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := ws.NewBridge(nil, nil, nil); err == nil {
		t.Error("nil deps should error")
	}
	hub := ws.NewHub(nil)
	if _, err := ws.NewBridge(hub, nil, nil); err == nil {
		t.Error("nil broker should error")
	}
	broker := pubsub.NewInProc(pubsub.NewRegistry())
	t.Cleanup(func() { _ = broker.Close() })
	if _, err := ws.NewBridge(nil, broker, nil); err == nil {
		t.Error("nil hub should error")
	}
}

func TestBridge_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub(nil)
	broker := pubsub.NewInProc(pubsub.NewRegistry())
	t.Cleanup(func() { _ = broker.Close() })
	b, err := ws.NewBridge(hub, broker, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	// Subscribe to start the dispatcher so Close has work to do.
	if err := b.Subscribe(context.Background(), uuid.Must(uuid.NewV7()), "conv:x:messages"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	b.Close()
	b.Close() // second call must not panic
}
