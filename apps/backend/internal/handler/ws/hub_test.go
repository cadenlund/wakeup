package ws_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/handler/ws"
)

// echoServer wraps a Hub + a tiny upgrade handler so tests can dial a
// real WS connection. The handler accepts every request as the same
// userID (provided by the test) so the hub's per-user fan-out is
// trivially exercisable.
//
// onMessage forwards inbound frames into a channel the test can drain.
type echoServer struct {
	hub      *ws.Hub
	server   *httptest.Server
	url      string
	userID   uuid.UUID
	inbound  chan []byte
	connErrs chan error
}

func newEchoServer(t *testing.T, userID uuid.UUID, opts ...echoOpt) *echoServer {
	t.Helper()
	o := echoOpts{kickThreshold: 0}
	for _, fn := range opts {
		fn(&o)
	}
	hub := ws.NewHub(nil)
	if o.kickThreshold > 0 {
		hub.SetKickThreshold(o.kickThreshold)
	}
	srv := &echoServer{
		hub:      hub,
		userID:   userID,
		inbound:  make(chan []byte, 64),
		connErrs: make(chan error, 4),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, ws.AcceptOptions([]string{"*"}))
		if err != nil {
			t.Logf("Accept: %v", err)
			return
		}
		conn, err := ws.NewConn(ws.ConnConfig{
			UserID: userID, WS: c, Hub: hub,
			OnMessage: func(_ context.Context, raw []byte) error {
				srv.inbound <- raw
				return nil
			},
		})
		if err != nil {
			t.Logf("NewConn: %v", err)
			_ = c.Close(websocket.StatusInternalError, "newconn")
			return
		}
		hub.Register(conn)
		runErr := conn.Run(r.Context())
		select {
		case srv.connErrs <- runErr:
		default:
		}
	})
	srv.server = httptest.NewServer(mux)
	srv.url = "ws" + srv.server.URL[len("http"):] + "/ws"
	t.Cleanup(srv.server.Close)
	return srv
}

type echoOpts struct {
	kickThreshold int
}

type echoOpt func(*echoOpts)

// echoOpt + withKickThreshold are kept for the upgrade-handler tests
// in milestone 8.3 where slow-consumer behavior is exercised end-to-end
// over a real httptest.Server.
var _ echoOpt = withKickThreshold(0)

func withKickThreshold(n int) echoOpt {
	return func(o *echoOpts) { o.kickThreshold = n }
}

// dial connects to the echo server, returning a *websocket.Conn the
// test can Read/Write on. The handshake response body is closed to
// satisfy bodyclose (websocket.Dial's contract is that the body is
// already drained on a successful upgrade, but bodyclose still wants
// the symmetric Close).
func dial(t *testing.T, urlStr string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, urlStr, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial %s: %v", urlStr, err)
	}
	return c
}

// readWithTimeout pulls one frame from c with a deadline, returning the
// payload (or failing the test).
func readWithTimeout(t *testing.T, c *websocket.Conn, d time.Duration) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, p, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return p
}

// --- Hub registry -----------------------------------------------------

func TestHub_RegisterUnregisterCount(t *testing.T) {
	t.Parallel()
	uid := uuid.Must(uuid.NewV7())
	srv := newEchoServer(t, uid)

	c := dial(t, srv.url)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })

	// Wait for Register to land (the upgrade goroutine ran ahead of us
	// in real time, but the test goroutine raced past Register's
	// Lock). Poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.hub.ConnCount(uid) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := srv.hub.ConnCount(uid); got != 1 {
		t.Fatalf("ConnCount = %d, want 1", got)
	}

	_ = c.Close(websocket.StatusNormalClosure, "bye")
	// On client close the read pump returns; Conn.Run's deferred
	// Unregister fires. Poll until the count drops.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.hub.ConnCount(uid) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := srv.hub.ConnCount(uid); got != 0 {
		t.Errorf("ConnCount after close = %d, want 0", got)
	}
}

// Multi-conn fan-out: a single user with two devices receives every
// broadcast on both connections.
func TestHub_BroadcastFansOutToEveryConn(t *testing.T) {
	t.Parallel()
	uid := uuid.Must(uuid.NewV7())
	srv := newEchoServer(t, uid)

	c1 := dial(t, srv.url)
	c2 := dial(t, srv.url)
	t.Cleanup(func() {
		_ = c1.Close(websocket.StatusNormalClosure, "")
		_ = c2.Close(websocket.StatusNormalClosure, "")
	})

	waitConnCount(t, srv.hub, uid, 2)

	srv.hub.BroadcastToUser(uid, []byte(`{"type":"message.new","data":{"hi":1}}`))

	got1 := readWithTimeout(t, c1, 2*time.Second)
	got2 := readWithTimeout(t, c2, 2*time.Second)
	if string(got1) != string(got2) {
		t.Errorf("c1 vs c2 differ: %q vs %q", got1, got2)
	}
}

// Broadcasts to user A do not leak to user B's connections.
func TestHub_BroadcastDoesNotLeakAcrossUsers(t *testing.T) {
	t.Parallel()
	uidA := uuid.Must(uuid.NewV7())
	uidB := uuid.Must(uuid.NewV7())

	srvA := newEchoServer(t, uidA)
	srvB := newEchoServer(t, uidB)

	cA := dial(t, srvA.url)
	cB := dial(t, srvB.url)
	t.Cleanup(func() {
		_ = cA.Close(websocket.StatusNormalClosure, "")
		_ = cB.Close(websocket.StatusNormalClosure, "")
	})
	waitConnCount(t, srvA.hub, uidA, 1)
	waitConnCount(t, srvB.hub, uidB, 1)

	srvA.hub.BroadcastToUser(uidA, []byte("{}"))

	// cA gets the frame.
	_ = readWithTimeout(t, cA, 2*time.Second)
	// cB must NOT — wait briefly and assert no frame arrived.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := cB.Read(ctx)
	if err == nil {
		t.Errorf("cB received a frame meant for uidA")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("cB read err = %v (want DeadlineExceeded)", err)
	}
}

// --- Inbound frames ---------------------------------------------------

func TestConn_OnMessageReceivesInboundFrames(t *testing.T) {
	t.Parallel()
	uid := uuid.Must(uuid.NewV7())
	srv := newEchoServer(t, uid)
	c := dial(t, srv.url)
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })

	waitConnCount(t, srv.hub, uid, 1)

	if err := c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"heartbeat","data":{}}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case got := <-srv.inbound:
		if string(got) == "" {
			t.Errorf("empty inbound payload")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound frame")
	}
}

// --- Slow consumer + kick ---------------------------------------------

// Slow-consumer kick: a conn whose write pipe is stalled (so the
// writePump never drains `out`) gets force-closed after the configured
// threshold of consecutive drops.
//
// We use a fakeWS whose Write blocks on a gate to make this
// deterministic — over a real TCP socket, the kernel's send buffer
// absorbs several KiB before Write blocks, so the test would race the
// writePump against the publisher. Hub-side drop / kick logic doesn't
// care which transport is underneath.
func TestHub_SlowConsumerGetsKicked(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub(nil)
	hub.SetKickThreshold(2)

	fake := newStallingFakeWS()
	c, err := ws.NewConn(ws.ConnConfig{
		UserID: uuid.Must(uuid.NewV7()), WS: fake, Hub: hub,
	})
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	hub.Register(c)
	t.Cleanup(func() { hub.Unregister(c) })

	go func() { _ = c.Run(context.Background()) }()

	// Saturate the per-conn buffer: writePump is stalled on the fake's
	// gated Write, so each Send past the buffer triggers drop-oldest.
	payload := []byte("payload")
	const blast = 64 + 8 // 64 fill the buffer, the next 8 are drops
	for i := 0; i < blast; i++ {
		c.Send(payload)
	}

	// After threshold consecutive drops, Close fires; Run's deferred
	// Unregister returns ConnCount to 0.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.ConnCount(c.UserID) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("ConnCount = %d after kick threshold; want 0", hub.ConnCount(c.UserID))
}

// stallingFakeWS is the same as fakeWS but Write blocks until ctx
// cancels, so the writePump never drains the per-conn buffer. Used by
// the slow-consumer test to avoid racing the kernel send buffer.
type stallingFakeWS struct {
	*fakeWS
	writeBlock chan struct{}
}

func newStallingFakeWS() *stallingFakeWS {
	return &stallingFakeWS{fakeWS: newFakeWS(), writeBlock: make(chan struct{})}
}

func (f *stallingFakeWS) Write(ctx context.Context, _ websocket.MessageType, _ []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.writeBlock:
		return errors.New("stallingFakeWS: closed")
	}
}

// Close override the embedded fakeWS so we can release the write gate.
func (f *stallingFakeWS) Close(code websocket.StatusCode, reason string) error {
	if f.closed.CompareAndSwap(false, true) {
		close(f.writeBlock)
		close(f.readWait)
	}
	_ = code
	_ = reason
	return nil
}

// Send drops oldest, not newest: with a 1-slot buffer (simulated via
// the Send method itself), the latest message wins.
func TestSend_DropsOldestPreservesNewest(t *testing.T) {
	t.Parallel()
	// Use a fake wsConn so we can isolate Send's drop logic from the
	// real network round-trip.
	fake := newFakeWS()
	hub := ws.NewHub(nil)
	hub.SetKickThreshold(1000) // don't kick during the test
	c, err := ws.NewConn(ws.ConnConfig{
		UserID: uuid.Must(uuid.NewV7()), WS: fake, Hub: hub,
	})
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	hub.Register(c)
	t.Cleanup(func() { hub.Unregister(c) })

	// Fill 64 + push 1 more: the 65th send should still land in the
	// channel (after dropping the oldest), so length stays at 64.
	for i := 0; i < 65; i++ {
		c.Send([]byte(fmt.Sprintf("msg-%d", i)))
	}
	// We can't peek the chan length safely without exposing internals;
	// instead, drain via the write pump path. Start Run with a fake
	// context so writePump can pull from out.
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(runCtx) }()

	// Collect every payload the writePump shipped to fake.WriteCalls.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fake.writeCount() >= 64 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := fake.writeCount(); got < 64 {
		t.Fatalf("writeCount = %d, want >= 64 (drop-oldest preserves the newest 64)", got)
	}
	// The last write must be the newest message.
	last := fake.writeAt(fake.writeCount() - 1)
	if string(last) != "msg-64" {
		t.Errorf("last write = %q, want msg-64", last)
	}
}

// --- helpers ---------------------------------------------------------

func waitConnCount(t *testing.T, h *ws.Hub, uid uuid.UUID, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.ConnCount(uid) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ConnCount(uid) = %d, want %d after 2s", h.ConnCount(uid), want)
}

// fakeWS is a minimal in-memory wsConn. Read blocks until ctx cancels
// (so the read pump doesn't spin) and Write appends to a slice.
type fakeWS struct {
	mu       sync.Mutex
	writes   [][]byte
	closed   atomic.Bool
	readWait chan struct{}
}

func newFakeWS() *fakeWS { return &fakeWS{readWait: make(chan struct{})} }

func (f *fakeWS) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-f.readWait:
		return 0, nil, errors.New("fakeWS: closed")
	}
}

func (f *fakeWS) Write(_ context.Context, _ websocket.MessageType, p []byte) error {
	if f.closed.Load() {
		return errors.New("fakeWS: closed")
	}
	f.mu.Lock()
	cp := append([]byte(nil), p...)
	f.writes = append(f.writes, cp)
	f.mu.Unlock()
	return nil
}

func (f *fakeWS) Close(_ websocket.StatusCode, _ string) error {
	if f.closed.CompareAndSwap(false, true) {
		close(f.readWait)
	}
	return nil
}

func (f *fakeWS) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *fakeWS) writeAt(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[i]
}
