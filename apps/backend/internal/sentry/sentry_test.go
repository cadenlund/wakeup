package sentry_test

import (
	"errors"
	"testing"
	"time"

	sentryclient "github.com/cadenlund/wakeup/apps/backend/internal/sentry"
)

// New rejects an empty DSN so missing-secret rollouts in non-dev envs
// fail fast at startup. cmd/server's buildSentry handles the dev case
// by returning nil before reaching New; this test pins the contract.
func TestNew_RejectsEmptyDSN(t *testing.T) {
	t.Parallel()
	for _, dsn := range []string{"", "   ", "\t\n"} {
		if _, err := sentryclient.New(sentryclient.Config{DSN: dsn}); err == nil {
			t.Errorf("New(%q): expected error, got nil", dsn)
		}
	}
}

// A nil-receiver Capture is a no-op — buildSentry passes nil to the
// recovery middleware in dev, and the recovery code paths assume that
// a nil Capturer is safe to call without crashing.
func TestCapture_NilReceiverNoOp(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Capture on nil client should not panic, got %v", r)
		}
	}()
	var c *sentryclient.Client
	c.Capture(errors.New("boom"), map[string]string{"k": "v"})
}

// Flush on a nil client returns true (nothing to drain) — matches the
// SDK contract where a no-op flush is "successful".
func TestFlush_NilReceiverReturnsTrue(t *testing.T) {
	t.Parallel()
	var c *sentryclient.Client
	if !c.Flush(time.Second) {
		t.Error("nil-receiver Flush should return true")
	}
}
