package pubsub

import (
	"context"
	"errors"
	"sync"
)

// Registry is the shared state every InProcBroker connected to it sees.
// In a single-process test you build one Registry and hand it to N brokers
// that simulate N backend replicas: a Publish on broker A will be delivered
// to every subscribed broker B's output chan.
//
// The zero value is NOT usable — call NewRegistry.
type Registry struct {
	mu      sync.RWMutex
	brokers map[*InProcBroker]struct{}
}

// NewRegistry returns an empty registry. Pass it to NewInProc when building
// brokers that need to communicate.
func NewRegistry() *Registry {
	return &Registry{brokers: map[*InProcBroker]struct{}{}}
}

// register adds b to the registry. Called by NewInProc.
func (r *Registry) register(b *InProcBroker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.brokers[b] = struct{}{}
}

// unregister removes b from the registry. Called by InProcBroker.Close.
func (r *Registry) unregister(b *InProcBroker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.brokers, b)
}

// fanout walks every registered broker and delivers msg to those whose
// active channel set includes msg.Channel. Each broker's deliver call
// takes its own lock — registry's read lock holds for traversal only.
func (r *Registry) fanout(msg Message) {
	r.mu.RLock()
	targets := make([]*InProcBroker, 0, len(r.brokers))
	for b := range r.brokers {
		targets = append(targets, b)
	}
	r.mu.RUnlock()
	for _, b := range targets {
		b.deliver(msg)
	}
}

// InProcBroker is the test/dev implementation of Broker. Internally it's
// a buffered output channel + a set of subscribed channels; the Registry
// hands incoming Publishes to every interested InProcBroker.
//
// The output chan has a fixed buffer; if a slow consumer fills it, further
// Publishes drop on the floor for that broker (matching Redis pubsub's
// fire-and-forget semantics — slow consumers don't backpressure publishers).
// The defaultBuf gives plenty of headroom for tests.
type InProcBroker struct {
	registry *Registry
	out      chan Message

	mu     sync.RWMutex
	subs   map[string]struct{}
	closed bool
}

const defaultBuf = 64

// NewInProc returns an InProcBroker bound to registry. Multiple brokers
// sharing one registry behave as separate replicas of the same bus.
func NewInProc(registry *Registry) *InProcBroker {
	if registry == nil {
		panic("pubsub: NewInProc called with nil registry")
	}
	b := &InProcBroker{
		registry: registry,
		out:      make(chan Message, defaultBuf),
		subs:     map[string]struct{}{},
	}
	registry.register(b)
	return b
}

// Publish hands the message to the registry; the registry fans out to
// every broker (including this one) whose subs include channel.
func (b *InProcBroker) Publish(_ context.Context, channel string, payload []byte) error {
	if b.isClosed() {
		return ErrClosed
	}
	// Defensive copy: callers commonly reuse the payload buffer.
	cp := append([]byte(nil), payload...)
	b.registry.fanout(Message{Channel: channel, Payload: cp})
	return nil
}

// Subscribe adds channels to this broker's active set and returns the
// shared output chan. Returning the SAME chan on every call (per the
// Broker contract) lets a caller add channels incrementally.
func (b *InProcBroker) Subscribe(_ context.Context, channels ...string) (<-chan Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	for _, c := range channels {
		b.subs[c] = struct{}{}
	}
	return b.out, nil
}

// Unsubscribe removes channels from the active set. Unknown channels are
// ignored (not an error) — matches Redis pubsub.
func (b *InProcBroker) Unsubscribe(_ context.Context, channels ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	for _, c := range channels {
		delete(b.subs, c)
	}
	return nil
}

// Close unregisters from the bus and closes the output chan. Safe to call
// multiple times.
func (b *InProcBroker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.registry.unregister(b)
	close(b.out)
	return nil
}

// deliver checks whether this broker is subscribed to msg.Channel and, if
// so, drops msg into the output chan. Drops silently if the chan is full
// (slow-consumer protection per the Redis-equivalent semantics).
func (b *InProcBroker) deliver(msg Message) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	if _, ok := b.subs[msg.Channel]; !ok {
		return
	}
	select {
	case b.out <- msg:
	default:
		// Buffer full → drop. Prevents one slow subscriber from
		// stalling the whole fanout.
	}
}

func (b *InProcBroker) isClosed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.closed
}

// ErrClosed is returned when Publish/Subscribe/Unsubscribe is called on a
// broker that has already been closed.
var ErrClosed = errors.New("pubsub: broker is closed")

// Compile-time guard that InProcBroker implements Broker.
var _ Broker = (*InProcBroker)(nil)
