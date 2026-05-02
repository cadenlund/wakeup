package pubsub

import (
	"context"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"
)

// RedisBroker is the production Broker. One instance owns one *redis.PubSub
// connection; Subscribe/Unsubscribe drive that connection's channel set;
// the receive goroutine forwards incoming Redis Messages to the output chan.
//
// Slow-consumer policy matches InProcBroker: if the output chan fills, new
// messages drop silently. The hub on the other end is responsible for not
// blocking — typical pattern is per-conn worker goroutines that quickly
// move events to the WS write loop and don't queue indefinitely.
type RedisBroker struct {
	client *redis.Client
	out    chan Message

	mu     sync.Mutex
	pubsub *redis.PubSub
	closed bool
	cancel context.CancelFunc
	done   chan struct{}
}

// NewRedis returns a RedisBroker backed by the given client. The broker
// opens a long-lived PubSub connection on first Subscribe; until then no
// network resources are held.
func NewRedis(client *redis.Client) *RedisBroker {
	return &RedisBroker{
		client: client,
		out:    make(chan Message, defaultBuf),
		done:   make(chan struct{}),
	}
}

// Publish forwards to redis.Client.Publish.
func (b *RedisBroker) Publish(ctx context.Context, channel string, payload []byte) error {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if err := b.client.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("pubsub redis: publish %q: %w", channel, err)
	}
	return nil
}

// Subscribe lazily opens the underlying PubSub connection on first call,
// then subscribes to the requested channels. Subsequent calls add more
// channels to the same connection. Returns the same output chan every call.
func (b *RedisBroker) Subscribe(ctx context.Context, channels ...string) (<-chan Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	if b.pubsub == nil {
		b.pubsub = b.client.Subscribe(ctx, channels...)
		// Spawn the read loop on first Subscribe. cancel is used by
		// Close to make the loop exit even if Redis is hung.
		runCtx, cancel := context.WithCancel(context.Background())
		b.cancel = cancel
		go b.readLoop(runCtx)
	} else if len(channels) > 0 {
		if err := b.pubsub.Subscribe(ctx, channels...); err != nil {
			return nil, fmt.Errorf("pubsub redis: subscribe: %w", err)
		}
	}
	return b.out, nil
}

// Unsubscribe removes channels from the underlying PubSub. No-op if the
// PubSub hasn't been opened yet (no Subscribe calls happened).
func (b *RedisBroker) Unsubscribe(ctx context.Context, channels ...string) error {
	b.mu.Lock()
	ps := b.pubsub
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if ps == nil || len(channels) == 0 {
		return nil
	}
	if err := ps.Unsubscribe(ctx, channels...); err != nil {
		return fmt.Errorf("pubsub redis: unsubscribe: %w", err)
	}
	return nil
}

// Close shuts the PubSub connection and the read loop. Idempotent.
func (b *RedisBroker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	ps := b.pubsub
	cancel := b.cancel
	b.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	var err error
	if ps != nil {
		err = ps.Close()
		// Wait for the read loop to drain before closing the output chan.
		<-b.done
	}
	close(b.out)
	if err != nil {
		return fmt.Errorf("pubsub redis: close: %w", err)
	}
	return nil
}

// readLoop forwards messages from the PubSub channel to the output chan.
// Exits when the PubSub closes (Channel returns) or ctx is cancelled.
func (b *RedisBroker) readLoop(ctx context.Context) {
	defer close(b.done)

	b.mu.Lock()
	ch := b.pubsub.Channel()
	b.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			select {
			case b.out <- Message{Channel: m.Channel, Payload: []byte(m.Payload)}:
			default:
				// Slow-consumer drop, see type comment.
			}
		}
	}
}

// Compile-time guard that RedisBroker implements Broker.
var _ Broker = (*RedisBroker)(nil)
