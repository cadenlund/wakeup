// Package ws hosts the WebSocket hub + connection plumbing for §7
// realtime. The upgrade handler (8.3) constructs *Conn from an upgraded
// nhooyr.io/websocket socket and registers it with the *Hub; from there
// the hub fans out per-user broadcasts to every active connection.
//
// Phase 8.1 lands the in-process primitives only. Pubsub-driven
// fan-out across replicas lands in 8.2; the /v1/ws upgrade handler in
// 8.3.
//
// Concurrency model (§7.4):
//
//   - One goroutine per connection: a write pump that reads from a
//     bounded `out` channel and writes to the wire, plus a read pump
//     that drains the wire and either feeds an inbound handler or
//     closes the conn on EOF / parse error.
//
//   - The Hub holds the user_id → []*Conn map under an RWMutex. Reads
//     (BroadcastToUser, ConnCount) take RLock; writes (Register /
//     Unregister) take Lock.
//
//   - Slow-consumer policy: each conn has a 64-message write buffer.
//     When BroadcastToUser pushes to a full buffer, we DROP THE OLDEST
//     message (consistent with "fire-and-forget pubsub" semantics; a
//     stalled subscriber can't backpressure publishers). After
//     `slowConsumerKickThreshold` consecutive drops on one conn, we
//     close it so the client reconnects fresh.
package ws

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// writeBufferSize is the per-connection bounded write channel size
// (§7.4). Dimensioned so a brief publisher burst doesn't immediately
// trigger drops, while still bounding worst-case memory per conn.
const writeBufferSize = 64

// slowConsumerKickThreshold is the number of consecutive drops before
// the hub force-closes a conn so the client reconnects (§7.4 "force
// reconnect after threshold"). Tests override via Hub.SetKickThreshold
// so the threshold is testable without overshooting timeouts.
const slowConsumerKickThreshold = 16

// Hub owns the per-user → connections registry.
type Hub struct {
	mu     sync.RWMutex
	conns  map[uuid.UUID]map[*Conn]struct{}
	logger *slog.Logger

	// kickThreshold mirrors slowConsumerKickThreshold but is mutable
	// for tests. Reads are uncontended (no atomic needed) — the field
	// is only ever written before any conn registers.
	kickThreshold int
}

// NewHub returns an empty hub. Logger defaults to slog.Default when nil.
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		conns:         map[uuid.UUID]map[*Conn]struct{}{},
		logger:        logger,
		kickThreshold: slowConsumerKickThreshold,
	}
}

// SetKickThreshold overrides the slow-consumer kick threshold. Only
// safe to call before any Conn is registered (tests use it during
// fixture setup).
func (h *Hub) SetKickThreshold(n int) {
	if n <= 0 {
		return
	}
	h.kickThreshold = n
}

// Register adds c to the user's connection set. Idempotent on a
// already-registered conn.
func (h *Hub) Register(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.conns[c.UserID]
	if !ok {
		set = map[*Conn]struct{}{}
		h.conns[c.UserID] = set
	}
	set[c] = struct{}{}
}

// Unregister removes c from the user's set. Empty user buckets get
// pruned so ConnCount is honest. Idempotent.
func (h *Hub) Unregister(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.conns[c.UserID]
	if !ok {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.conns, c.UserID)
	}
}

// ConnCount returns the number of live conns registered for userID.
// Lock-free for callers — uses RLock under the hood.
func (h *Hub) ConnCount(userID uuid.UUID) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns[userID])
}

// BroadcastToUser pushes payload onto every active conn registered for
// userID. Drops oldest on a full per-conn buffer; closes a conn that
// has accumulated kickThreshold consecutive drops (§7.4 slow-consumer
// kick). Safe for the caller to call concurrently from any goroutine
// (typically the pubsub subscriber loop in 8.2).
func (h *Hub) BroadcastToUser(userID uuid.UUID, payload []byte) {
	h.mu.RLock()
	conns := make([]*Conn, 0, len(h.conns[userID]))
	for c := range h.conns[userID] {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.Send(payload)
	}
}

// Conn is one upgraded WebSocket connection. The upgrade handler
// constructs a Conn, registers it with the Hub, then calls Run to
// start the pumps.
type Conn struct {
	// UserID is the authenticated user this conn belongs to.
	UserID uuid.UUID
	// ID is a stable per-conn identifier surfaced in logs so a single
	// user with multiple devices is debuggable.
	ID uuid.UUID

	hub    *Hub
	ws     wsConn
	out    chan []byte
	logger *slog.Logger

	// onMessage is the inbound-event hook the upgrade handler wires
	// (heartbeat / typing / presence.set in milestone 8.3). nil means
	// drop-with-debug-log.
	onMessage func(ctx context.Context, raw []byte) error

	// drops counts consecutive Send() drops for this conn — reset to
	// zero on every successful enqueue. When >= hub.kickThreshold the
	// conn force-closes via the cancel below.
	drops atomic.Int64

	closeOnce sync.Once

	// closeMu guards cancelCtx — Run() writes it during startup while
	// Close() (called from Send's slow-consumer kick on a publisher
	// goroutine) reads it. Without the mutex the race detector flags
	// the read/write on cancelCtx itself plus the internal mutation
	// inside context.WithCancel.
	closeMu   sync.Mutex
	cancelCtx context.CancelFunc
}

// wsConn is the slice of nhooyr.io/websocket.Conn the hub needs.
// Narrowing the surface keeps the test fake small.
type wsConn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, mt websocket.MessageType, p []byte) error
	Close(code websocket.StatusCode, reason string) error
}

// ConnConfig builds a Conn.
type ConnConfig struct {
	UserID    uuid.UUID
	WS        wsConn
	Hub       *Hub
	Logger    *slog.Logger
	OnMessage func(ctx context.Context, raw []byte) error
}

// NewConn builds a Conn. Hub + WS + UserID are required.
func NewConn(cfg ConnConfig) (*Conn, error) {
	if cfg.Hub == nil {
		return nil, errors.New("ws: ConnConfig.Hub is required")
	}
	if cfg.WS == nil {
		return nil, errors.New("ws: ConnConfig.WS is required")
	}
	if cfg.UserID == uuid.Nil {
		return nil, errors.New("ws: ConnConfig.UserID is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = cfg.Hub.logger
	}
	return &Conn{
		UserID:    cfg.UserID,
		ID:        uuid.Must(uuid.NewV7()),
		hub:       cfg.Hub,
		ws:        cfg.WS,
		out:       make(chan []byte, writeBufferSize),
		logger:    logger,
		onMessage: cfg.OnMessage,
	}, nil
}

// Send queues payload onto the conn's write buffer. On a full buffer,
// drop-oldest then enqueue (§7.4). Each successful enqueue resets the
// consecutive-drop counter; reaching kickThreshold triggers Close so
// the client reconnects fresh.
//
// Defensive copy: callers (BroadcastToUser, the pubsub subscriber loop
// in 8.2) often reuse the payload buffer or hand us a slice backed by
// a pubsub.Message that goes back into a pool. If we sent the same
// backing array onto the channel, writePump would race with the next
// caller-side mutation. Always own the bytes we hand off.
func (c *Conn) Send(payload []byte) {
	cp := make([]byte, len(payload))
	copy(cp, payload)
	select {
	case c.out <- cp:
		c.drops.Store(0)
		return
	default:
	}
	// Buffer full → pop oldest, then push new. Use a non-blocking
	// receive so we don't accidentally block another goroutine racing
	// to drain.
	select {
	case <-c.out:
	default:
	}
	select {
	case c.out <- cp:
	default:
		// In the unlikely race where another sender refilled the slot
		// between the receive and the send, count this as a hard drop.
	}
	if c.drops.Add(1) >= int64(c.hub.kickThreshold) {
		c.logger.Warn("ws: slow consumer; closing conn",
			slog.String("user_id", c.UserID.String()),
			slog.String("conn_id", c.ID.String()),
			slog.Int("drops", int(c.drops.Load())),
		)
		c.Close()
	}
}

// Run starts the read + write pumps and blocks until either returns.
// On exit the conn is unregistered + the underlying socket closed.
//
// Caller is the /v1/ws upgrade handler: it owns the request context, so
// a request cancellation propagates here and tears the conn down.
func (c *Conn) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	c.closeMu.Lock()
	c.cancelCtx = cancel
	c.closeMu.Unlock()
	defer cancel()
	defer c.hub.Unregister(c)
	defer func() { _ = c.ws.Close(websocket.StatusNormalClosure, "bye") }()

	errs := make(chan error, 2)
	go func() { errs <- c.writePump(runCtx) }()
	go func() { errs <- c.readPump(runCtx) }()

	// First pump to return wins; cancel the runCtx so the other pump
	// also unblocks. We surface only the first error; the second is
	// drained but not returned.
	first := <-errs
	cancel()
	<-errs
	if first != nil && !errors.Is(first, context.Canceled) {
		return first
	}
	return nil
}

// Close force-closes the connection. Idempotent.
func (c *Conn) Close() {
	c.closeOnce.Do(func() {
		c.closeMu.Lock()
		cancel := c.cancelCtx
		c.closeMu.Unlock()
		if cancel != nil {
			cancel()
		}
		_ = c.ws.Close(websocket.StatusPolicyViolation, "slow consumer")
	})
}

// writePump drains c.out and writes each payload as a Text frame.
// Returns on ctx done or write error (a write error means the client
// disconnected; nothing else to do).
func (c *Conn) writePump(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case payload, ok := <-c.out:
			if !ok {
				return nil
			}
			if err := c.ws.Write(ctx, websocket.MessageText, payload); err != nil {
				return err
			}
		}
	}
}

// readPump drains the wire. Each frame is handed to onMessage if one
// was wired; otherwise it's logged at debug and dropped (the read
// itself is what keeps the WebSocket alive — pings happen at the
// nhooyr layer). Returns on ctx done or any read error (any read error
// means the connection has closed from the client side).
func (c *Conn) readPump(ctx context.Context) error {
	for {
		_, raw, err := c.ws.Read(ctx)
		if err != nil {
			return err
		}
		if c.onMessage == nil {
			c.logger.Debug("ws: inbound frame dropped (no handler)",
				slog.String("user_id", c.UserID.String()),
				slog.Int("bytes", len(raw)),
			)
			continue
		}
		if err := c.onMessage(ctx, raw); err != nil {
			// Don't kill the loop on a single bad frame — the upgrade
			// handler decides whether a malformed event is fatal.
			c.logger.Warn("ws: inbound handler error",
				slog.String("user_id", c.UserID.String()),
				slog.String("error", err.Error()),
			)
		}
	}
}

// AcceptOptions returns the nhooyr.io/websocket options the upgrade
// handler should use. Centralized here so 8.3 doesn't need to remember
// the §8.4 origin policy. CompressionMode disabled per nhooyr's note
// about CVE risk in older browsers.
func AcceptOptions(allowedOrigins []string) *websocket.AcceptOptions {
	return &websocket.AcceptOptions{
		OriginPatterns:  allowedOrigins,
		CompressionMode: websocket.CompressionDisabled,
	}
}

// Compile-time guard that *websocket.Conn satisfies the wsConn
// interface — keeps the upgrade handler honest if nhooyr ever
// renames a method.
var _ wsConn = (*websocket.Conn)(nil)

// MustHandshakeRejected is a small helper for tests / health checks
// that want to assert the upgrade handler refused a handshake. It
// peeks at the response status without holding open a connection.
func MustHandshakeRejected(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	return resp.StatusCode != http.StatusSwitchingProtocols
}
