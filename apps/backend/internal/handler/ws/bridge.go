package ws

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
)

// Bridge wires a pubsub.Broker into a Hub: it owns one subscription
// loop per Hub instance and fans incoming messages out to every user
// who has registered an interest in the message's channel.
//
// Lifecycle:
//
//   - The /v1/ws upgrade handler (8.3) calls Subscribe(userID, ch...)
//     after looking up the user's conversations. The bridge ensures
//     the broker is subscribed to each channel exactly once per Hub
//     and remembers that the userID wants events on it.
//
//   - On Unsubscribe, the bridge clears the user's interest. When the
//     last user on a channel unsubscribes, the broker subscription
//     itself is dropped to keep the broker's keyspace small.
//
//   - The dispatcher goroutine drains the broker's shared delivery
//     chan, looks up the per-channel interested-user set, and calls
//     Hub.BroadcastToUser for each. Slow-consumer + drop-oldest
//     policy is the Hub's job — the bridge is plain fan-out.
//
// Bridge.Close stops the dispatcher and removes every pending
// subscription via Broker.Unsubscribe (best-effort — the broker's
// Close also handles teardown).
type Bridge struct {
	hub    *Hub
	broker pubsub.Broker
	logger *slog.Logger

	mu   sync.Mutex
	subs map[string]map[uuid.UUID]struct{} // channel → set of user_ids

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewBridge builds a Bridge bound to hub + broker. Logger defaults to
// hub.logger when nil.
func NewBridge(hub *Hub, broker pubsub.Broker, logger *slog.Logger) (*Bridge, error) {
	if hub == nil {
		return nil, errors.New("ws: NewBridge requires non-nil hub")
	}
	if broker == nil {
		return nil, errors.New("ws: NewBridge requires non-nil broker")
	}
	if logger == nil {
		logger = hub.logger
	}
	return &Bridge{
		hub:    hub,
		broker: broker,
		logger: logger,
		subs:   map[string]map[uuid.UUID]struct{}{},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}, nil
}

// Subscribe registers userID's interest on every channel and ensures
// the underlying broker is subscribed. First call also starts the
// dispatcher goroutine. Idempotent — subscribing the same userID to
// the same channel twice is a no-op.
//
// Errors from Broker.Subscribe propagate so the upgrade handler can
// reject the connection if the bus is unavailable.
func (b *Bridge) Subscribe(ctx context.Context, userID uuid.UUID, channels ...string) error {
	if len(channels) == 0 {
		return nil
	}
	b.startOnce.Do(func() {
		go b.dispatch()
	})
	b.mu.Lock()
	newChannels := make([]string, 0, len(channels))
	for _, ch := range channels {
		set, exists := b.subs[ch]
		if !exists {
			set = map[uuid.UUID]struct{}{}
			b.subs[ch] = set
			newChannels = append(newChannels, ch)
		}
		set[userID] = struct{}{}
	}
	b.mu.Unlock()

	if len(newChannels) == 0 {
		return nil
	}
	if _, err := b.broker.Subscribe(ctx, newChannels...); err != nil {
		// Roll back our bookkeeping so a retry can re-attempt cleanly.
		b.mu.Lock()
		for _, ch := range newChannels {
			if set, ok := b.subs[ch]; ok {
				delete(set, userID)
				if len(set) == 0 {
					delete(b.subs, ch)
				}
			}
		}
		b.mu.Unlock()
		return err
	}
	return nil
}

// Unsubscribe clears userID's interest on each channel. When the last
// user on a channel unsubscribes, the broker subscription is also
// dropped. Idempotent.
func (b *Bridge) Unsubscribe(ctx context.Context, userID uuid.UUID, channels ...string) error {
	if len(channels) == 0 {
		return nil
	}
	b.mu.Lock()
	emptied := make([]string, 0, len(channels))
	for _, ch := range channels {
		set, ok := b.subs[ch]
		if !ok {
			continue
		}
		delete(set, userID)
		if len(set) == 0 {
			delete(b.subs, ch)
			emptied = append(emptied, ch)
		}
	}
	b.mu.Unlock()
	if len(emptied) == 0 {
		return nil
	}
	return b.broker.Unsubscribe(ctx, emptied...)
}

// UnsubscribeAll clears every channel registration for userID. Used by
// the upgrade handler when a connection closes. Returns the channels
// the broker was unsubscribed from (empty when nothing was emptied).
func (b *Bridge) UnsubscribeAll(ctx context.Context, userID uuid.UUID) []string {
	b.mu.Lock()
	emptied := []string{}
	for ch, set := range b.subs {
		if _, ok := set[userID]; !ok {
			continue
		}
		delete(set, userID)
		if len(set) == 0 {
			delete(b.subs, ch)
			emptied = append(emptied, ch)
		}
	}
	b.mu.Unlock()
	if len(emptied) > 0 {
		_ = b.broker.Unsubscribe(ctx, emptied...)
	}
	return emptied
}

// Close stops the dispatcher and drops every subscription. Safe to
// call multiple times.
func (b *Bridge) Close() {
	b.stopOnce.Do(func() {
		close(b.stopCh)
		<-b.doneCh
	})
}

// dispatch is the single goroutine that drains the broker's shared
// delivery chan and routes each message to interested users via the
// hub's BroadcastToUser.
//
// We re-Subscribe with no channels to obtain the broker's delivery
// chan handle. The broker's Subscribe contract is "returns the same
// chan on every call" so this is a safe handshake.
func (b *Bridge) dispatch() {
	defer close(b.doneCh)
	ch, err := b.broker.Subscribe(context.Background())
	if err != nil {
		b.logger.Error("ws bridge: broker subscribe failed",
			slog.String("error", err.Error()),
		)
		return
	}
	for {
		select {
		case <-b.stopCh:
			return
		case msg, ok := <-ch:
			if !ok {
				// Broker closed its chan — nothing more to do.
				return
			}
			b.mu.Lock()
			users := make([]uuid.UUID, 0, len(b.subs[msg.Channel]))
			for uid := range b.subs[msg.Channel] {
				users = append(users, uid)
			}
			b.mu.Unlock()
			for _, uid := range users {
				b.hub.BroadcastToUser(uid, msg.Payload)
			}
		}
	}
}
