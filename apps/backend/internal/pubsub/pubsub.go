// Package pubsub abstracts the cross-instance message bus the WebSocket hub
// uses to fan WS events out across backend replicas (§4.5). Two
// implementations:
//
//   - RedisBroker — production, backed by *redis.Client.Subscribe.
//   - InProcBroker — single-process tests + dev. Multiple InProcBrokers
//     constructed from the same Registry communicate as if they shared
//     one Redis instance.
//
// Channels follow the §4.5 naming convention:
//
//	user:<id>:events       — events for one user (presence, friend req, etc.)
//	conv:<id>:messages     — events for a conversation
package pubsub

import "context"

// Message is one unit of pub/sub delivery. Channel is the channel the
// subscriber matched on (so multiplexing many channels into one delivery
// chan still tells the consumer which sender it came from).
type Message struct {
	Channel string
	Payload []byte
}

// Broker is the contract every pub/sub backend satisfies. Multiple
// Subscribe calls on the same broker accumulate the channel set; the
// returned chan is broker-wide (one Subscription per broker).
//
// Closing the broker (via Close) shuts the underlying transport, drains
// pending deliveries, and closes the output channel.
type Broker interface {
	// Publish sends payload to every subscriber listening on channel.
	// In Redis-mode the call is fire-and-forget at the SDK level.
	Publish(ctx context.Context, channel string, payload []byte) error

	// Subscribe adds the given channels to this broker's active
	// subscription set. The returned <-chan Message receives every
	// message published to any subscribed channel for as long as the
	// broker is open. The same channel value is returned on every call
	// (subscriptions are broker-scoped, not per-call).
	Subscribe(ctx context.Context, channels ...string) (<-chan Message, error)

	// Unsubscribe removes channels from the active set. Calling with a
	// channel the broker isn't subscribed to is a no-op (not an error).
	Unsubscribe(ctx context.Context, channels ...string) error

	// Close stops the broker, releases the underlying transport (Redis
	// pubsub conn or in-proc registration), and closes the delivery chan.
	// Safe to call multiple times.
	Close() error
}
