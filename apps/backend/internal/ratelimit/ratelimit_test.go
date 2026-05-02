package ratelimit_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/ratelimit"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// newClient gives each test a fresh *redis.Client pointed at the
// shared testcontainer redis (sync.Once-cached at the testutil layer).
func newClient(t *testing.T) *redis.Client {
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

// keyFor returns a cross-test-unique key so parallel tests can share the
// containerized redis without contaminating each other's bucket state.
func keyFor(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("test:%s:%s:%d", t.Name(), suffix, time.Now().UnixNano())
}

func TestAllow_BurstAllowed(t *testing.T) {
	t.Parallel()
	l := ratelimit.New(newClient(t))
	ctx := context.Background()
	key := keyFor(t, "burst")

	// 5 requests with limit=5, all should pass.
	for i := 0; i < 5; i++ {
		ok, retry, err := l.Allow(ctx, key, 5, time.Minute)
		if err != nil {
			t.Fatalf("req %d: err = %v", i, err)
		}
		if !ok {
			t.Fatalf("req %d: should be allowed (retryAfter=%v)", i, retry)
		}
	}
}

func TestAllow_SustainedLimitDeny(t *testing.T) {
	t.Parallel()
	l := ratelimit.New(newClient(t))
	ctx := context.Background()
	key := keyFor(t, "sustained")

	for i := 0; i < 5; i++ {
		ok, _, err := l.Allow(ctx, key, 5, time.Minute)
		if err != nil || !ok {
			t.Fatalf("setup req %d: ok=%v err=%v", i, ok, err)
		}
	}
	// 6th must deny with a positive retryAfter.
	ok, retry, err := l.Allow(ctx, key, 5, time.Minute)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if ok {
		t.Fatal("6th req should be denied")
	}
	if retry <= 0 {
		t.Fatalf("retryAfter should be > 0, got %v", retry)
	}
	// retryAfter should not exceed the window.
	if retry > time.Minute {
		t.Fatalf("retryAfter should not exceed window, got %v", retry)
	}
}

func TestAllow_RecoveryAfterWindow(t *testing.T) {
	t.Parallel()
	l := ratelimit.New(newClient(t))
	ctx := context.Background()
	key := keyFor(t, "recovery")

	// Tiny window so the test runs in <1s.
	const window = 200 * time.Millisecond

	// Saturate.
	for i := 0; i < 3; i++ {
		ok, _, err := l.Allow(ctx, key, 3, window)
		if err != nil || !ok {
			t.Fatalf("setup req %d: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, _, err := l.Allow(ctx, key, 3, window); err != nil || ok {
		t.Fatalf("4th should be denied; ok=%v err=%v", ok, err)
	}

	// Wait past the window — older entries roll out.
	time.Sleep(window + 50*time.Millisecond)

	if ok, _, err := l.Allow(ctx, key, 3, window); err != nil || !ok {
		t.Fatalf("after window: ok=%v err=%v", ok, err)
	}
}

func TestAllow_SeparateKeysAreIndependent(t *testing.T) {
	t.Parallel()
	l := ratelimit.New(newClient(t))
	ctx := context.Background()
	keyA := keyFor(t, "A")
	keyB := keyFor(t, "B")

	// Saturate keyA.
	for i := 0; i < 3; i++ {
		_, _, _ = l.Allow(ctx, keyA, 3, time.Minute)
	}
	if ok, _, _ := l.Allow(ctx, keyA, 3, time.Minute); ok {
		t.Fatal("keyA should be saturated")
	}

	// keyB must be unaffected.
	for i := 0; i < 3; i++ {
		ok, _, err := l.Allow(ctx, keyB, 3, time.Minute)
		if err != nil || !ok {
			t.Fatalf("keyB req %d: ok=%v err=%v", i, ok, err)
		}
	}
}

func TestBuildKey_Format(t *testing.T) {
	t.Parallel()
	got := ratelimit.BuildKey("auth", "192.0.2.1")
	want := "rl:auth:192.0.2.1"
	if got != want {
		t.Fatalf("BuildKey = %q, want %q", got, want)
	}
}

func TestAllow_RejectsBadInputs(t *testing.T) {
	t.Parallel()
	l := ratelimit.New(newClient(t))
	ctx := context.Background()
	key := keyFor(t, "bad-input")

	if _, _, err := l.Allow(ctx, key, 0, time.Minute); err == nil {
		t.Fatal("limit=0 should error")
	}
	if _, _, err := l.Allow(ctx, key, 5, 0); err == nil {
		t.Fatal("window=0 should error")
	}
	if _, _, err := l.Allow(ctx, "   ", 5, time.Minute); err == nil {
		t.Fatal("blank key should error")
	}
}

func TestNew_PanicsOnNilClient(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil client")
		}
	}()
	_ = ratelimit.New(nil)
}
