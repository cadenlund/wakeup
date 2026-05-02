package pubsub_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// newRedisClient builds a *redis.Client connected to the singleton
// testcontainer redis (cached via sync.Once). Each test that uses it gets
// a unique channel-name prefix so cross-test interference is impossible
// even though the Redis instance is shared.
func newRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	url := testutil.StartRedis(t)
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	c := redis.NewClient(opts)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// uniqueChannel returns a channel name no other parallel test will use.
// t.Name() is unique per test, and the timestamp guards against re-runs.
func uniqueChannel(t *testing.T) string {
	t.Helper()
	return "test:" + t.Name() + ":" + time.Now().Format("150405.000000")
}

func TestRedis_PublishSubscribeRoundTrip(t *testing.T) {
	t.Parallel()
	client := newRedisClient(t)
	b := pubsub.NewRedis(client)
	t.Cleanup(func() { _ = b.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := uniqueChannel(t)
	out, err := b.Subscribe(ctx, ch)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Redis pubsub Subscribe ack arrives asynchronously; wait briefly to
	// avoid a race where Publish happens before the server registers the
	// subscription. Empirically 50ms is plenty against a local container.
	time.Sleep(50 * time.Millisecond)

	if err := b.Publish(ctx, ch, []byte("payload-1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-out:
		if msg.Channel != ch {
			t.Errorf("Channel = %q", msg.Channel)
		}
		if string(msg.Payload) != "payload-1" {
			t.Errorf("Payload = %q", msg.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for redis message")
	}
}

func TestRedis_MultipleChannelsThroughOneBroker(t *testing.T) {
	t.Parallel()
	client := newRedisClient(t)
	b := pubsub.NewRedis(client)
	t.Cleanup(func() { _ = b.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	chA := uniqueChannel(t) + ":a"
	chB := uniqueChannel(t) + ":b"
	out, err := b.Subscribe(ctx, chA, chB)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := b.Publish(ctx, chA, []byte("A")); err != nil {
		t.Fatalf("Publish A: %v", err)
	}
	if err := b.Publish(ctx, chB, []byte("B")); err != nil {
		t.Fatalf("Publish B: %v", err)
	}

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		select {
		case msg := <-out:
			got[msg.Channel] = string(msg.Payload)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out at message %d", i)
		}
	}
	if got[chA] != "A" || got[chB] != "B" {
		t.Fatalf("muxed delivery wrong: %+v", got)
	}
}

func TestRedis_UnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()
	client := newRedisClient(t)
	b := pubsub.NewRedis(client)
	t.Cleanup(func() { _ = b.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := uniqueChannel(t)
	out, _ := b.Subscribe(ctx, ch)
	time.Sleep(50 * time.Millisecond)
	_ = b.Publish(ctx, ch, []byte("first"))

	select {
	case <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out draining first")
	}

	if err := b.Unsubscribe(ctx, ch); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let unsubscribe register
	_ = b.Publish(ctx, ch, []byte("second"))

	select {
	case msg := <-out:
		t.Fatalf("delivery after Unsubscribe: %+v", msg)
	case <-time.After(300 * time.Millisecond):
		// expected
	}
}

func TestRedis_TwoBrokersFanOut(t *testing.T) {
	t.Parallel()
	client := newRedisClient(t)
	a := pubsub.NewRedis(client)
	c := pubsub.NewRedis(client)
	t.Cleanup(func() { _ = a.Close() })
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := uniqueChannel(t)
	outA, _ := a.Subscribe(ctx, ch)
	outC, _ := c.Subscribe(ctx, ch)
	time.Sleep(50 * time.Millisecond)

	if err := a.Publish(ctx, ch, []byte("hi")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for name, out := range map[string]<-chan pubsub.Message{"a": outA, "c": outC} {
		select {
		case msg := <-out:
			if string(msg.Payload) != "hi" {
				t.Errorf("%s payload = %q", name, msg.Payload)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: timed out", name)
		}
	}
}

func TestRedis_CloseRejectsFurtherUse(t *testing.T) {
	t.Parallel()
	client := newRedisClient(t)
	b := pubsub.NewRedis(client)
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Idempotent: a second Close must also succeed (matches InProc parity).
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	ctx := context.Background()
	if _, err := b.Subscribe(ctx, "ch"); !errors.Is(err, pubsub.ErrClosed) {
		t.Fatalf("Subscribe after close: %v", err)
	}
	if err := b.Publish(ctx, "ch", nil); !errors.Is(err, pubsub.ErrClosed) {
		t.Fatalf("Publish after close: %v", err)
	}
}
