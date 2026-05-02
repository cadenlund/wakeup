package pubsub_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
)

func TestInProc_PublishSubscribeRoundTrip(t *testing.T) {
	t.Parallel()
	reg := pubsub.NewRegistry()
	b := pubsub.NewInProc(reg)
	t.Cleanup(func() { _ = b.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := b.Subscribe(ctx, "user:1:events")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := b.Publish(ctx, "user:1:events", []byte(`{"hi":"there"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-out:
		if msg.Channel != "user:1:events" {
			t.Errorf("Channel = %q", msg.Channel)
		}
		if string(msg.Payload) != `{"hi":"there"}` {
			t.Errorf("Payload = %q", msg.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestInProc_PublishToUnsubscribedChannelIsDropped(t *testing.T) {
	t.Parallel()
	reg := pubsub.NewRegistry()
	b := pubsub.NewInProc(reg)
	t.Cleanup(func() { _ = b.Close() })

	ctx := context.Background()
	out, _ := b.Subscribe(ctx, "user:1:events")
	if err := b.Publish(ctx, "user:2:events", []byte("for somebody else")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-out:
		t.Fatalf("unexpected message on unsubscribed channel: %+v", msg)
	case <-time.After(150 * time.Millisecond):
		// expected — no delivery
	}
}

// Multiple brokers attached to one Registry act like multiple replicas.
// A publish on broker A is delivered to every broker subscribed to that
// channel, including A itself.
func TestInProc_MultipleSubscribersOnSharedRegistry(t *testing.T) {
	t.Parallel()
	reg := pubsub.NewRegistry()
	a := pubsub.NewInProc(reg)
	c := pubsub.NewInProc(reg)
	t.Cleanup(func() { _ = a.Close() })
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	outA, _ := a.Subscribe(ctx, "conv:42:messages")
	outC, _ := c.Subscribe(ctx, "conv:42:messages")

	// Third broker NOT subscribed.
	other := pubsub.NewInProc(reg)
	t.Cleanup(func() { _ = other.Close() })
	outOther, _ := other.Subscribe(ctx, "different")

	if err := a.Publish(ctx, "conv:42:messages", []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for name, out := range map[string]<-chan pubsub.Message{"a": outA, "c": outC} {
		select {
		case msg := <-out:
			if string(msg.Payload) != "hello" {
				t.Errorf("%s: got %q", name, msg.Payload)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: timed out", name)
		}
	}

	// other must not see the message.
	select {
	case msg := <-outOther:
		t.Fatalf("subscriber on different channel saw: %+v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestInProc_UnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()
	reg := pubsub.NewRegistry()
	b := pubsub.NewInProc(reg)
	t.Cleanup(func() { _ = b.Close() })

	ctx := context.Background()
	out, _ := b.Subscribe(ctx, "ch")
	_ = b.Publish(ctx, "ch", []byte("first"))

	// Drain the first message.
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("timed out draining first")
	}

	if err := b.Unsubscribe(ctx, "ch"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	_ = b.Publish(ctx, "ch", []byte("second"))

	select {
	case msg := <-out:
		t.Fatalf("delivery after Unsubscribe: %+v", msg)
	case <-time.After(150 * time.Millisecond):
		// expected
	}
}

func TestInProc_UnsubscribeUnknownChannelIsNoOp(t *testing.T) {
	t.Parallel()
	reg := pubsub.NewRegistry()
	b := pubsub.NewInProc(reg)
	t.Cleanup(func() { _ = b.Close() })

	if err := b.Unsubscribe(context.Background(), "never-subscribed"); err != nil {
		t.Fatalf("Unsubscribe should be no-op for unknown channel, got %v", err)
	}
}

func TestInProc_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	reg := pubsub.NewRegistry()
	b := pubsub.NewInProc(reg)
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// Operations after close return ErrClosed.
	if _, err := b.Subscribe(context.Background(), "ch"); !errors.Is(err, pubsub.ErrClosed) {
		t.Fatalf("Subscribe after close: %v", err)
	}
	if err := b.Publish(context.Background(), "ch", nil); !errors.Is(err, pubsub.ErrClosed) {
		t.Fatalf("Publish after close: %v", err)
	}
}

// Concurrent publishers must not race the subscriber set under -race.
func TestInProc_ConcurrentPublishersAndSubscribers(t *testing.T) {
	t.Parallel()
	reg := pubsub.NewRegistry()
	b := pubsub.NewInProc(reg)
	t.Cleanup(func() { _ = b.Close() })

	ctx := context.Background()
	out, _ := b.Subscribe(ctx, "ch")

	var wg sync.WaitGroup
	const N = 50
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Publish(ctx, "ch", []byte("p"))
		}()
	}
	wg.Wait()

	// Drain whatever made it through. With buffer drops we don't expect
	// exactly N messages — we just want no panic / race report.
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-out:
		case <-timeout:
			return
		}
	}
}

func TestInProc_NewInProcPanicsOnNilRegistry(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil registry")
		}
	}()
	_ = pubsub.NewInProc(nil)
}
