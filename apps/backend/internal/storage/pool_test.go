package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// Tests that don't need a real postgres. The connect/Ping happy-path is
// covered by Phase 1.4 once testcontainers helpers exist.

func TestNewPool_RejectsEmptyURL(t *testing.T) {
	t.Parallel()
	_, err := storage.NewPool(context.Background(), storage.PoolConfig{})
	if err == nil {
		t.Fatal("expected error for empty DatabaseURL, got nil")
	}
}

func TestNewPool_RejectsMalformedURL(t *testing.T) {
	t.Parallel()
	// ParseConfig rejects strings that don't look like a postgres URL.
	_, err := storage.NewPool(context.Background(), storage.PoolConfig{
		DatabaseURL: "not-a-postgres-url",
	})
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestNewPool_RejectsInvalidKnobs(t *testing.T) {
	t.Parallel()
	const url = "postgres://user:pass@localhost:5432/db?sslmode=disable"
	cases := []struct {
		name   string
		cfg    storage.PoolConfig
		errSub string
	}{
		{
			name:   "negative MaxConns",
			cfg:    storage.PoolConfig{DatabaseURL: url, MaxConns: -1},
			errSub: "MaxConns",
		},
		{
			name:   "negative MinConns",
			cfg:    storage.PoolConfig{DatabaseURL: url, MinConns: -3},
			errSub: "MinConns",
		},
		{
			name:   "negative MaxConnLifetime",
			cfg:    storage.PoolConfig{DatabaseURL: url, MaxConnLifetime: -time.Second},
			errSub: "MaxConnLifetime",
		},
		{
			name:   "negative MaxConnIdleTime",
			cfg:    storage.PoolConfig{DatabaseURL: url, MaxConnIdleTime: -time.Hour},
			errSub: "MaxConnIdleTime",
		},
		{
			name:   "MinConns greater than MaxConns",
			cfg:    storage.PoolConfig{DatabaseURL: url, MinConns: 10, MaxConns: 5},
			errSub: "MinConns",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := storage.NewPool(context.Background(), tc.cfg)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("expected error to mention %q, got: %v", tc.errSub, err)
			}
		})
	}
}

func TestHealthCheck_NilPool(t *testing.T) {
	t.Parallel()
	err := storage.HealthCheck(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil pool, got nil")
	}
	// Sanity: error message should hint at the root cause for an operator
	// reading slog output, but don't pin the exact text.
	if errors.Is(err, context.Canceled) {
		t.Fatalf("got unexpected wrapped context error: %v", err)
	}
}

// HealthCheck happy + bounded-failure paths against a real pool. Uses
// testutil.NewTestDB so the assertions cover the Ping success branch
// and the canceled-context branch — both of which the nil-pool test
// can't reach.
func TestHealthCheck_RealPool(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	if err := storage.HealthCheck(context.Background(), pool); err != nil {
		t.Errorf("HealthCheck on live pool: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := storage.HealthCheck(canceledCtx, pool); err == nil {
		t.Error("HealthCheck on canceled ctx should error")
	}
}

// NewPool succeeds against a live postgres URL — exercises the
// pgxpool.NewWithConfig + Ping success path that the validation
// tests above can't reach. Uses the testutil-bound database URL so
// the test runs in the same env as the rest of the suite.
//
// Also asserts non-default knobs (MaxConns, MaxConnLifetime, etc.)
// land on the pool's config — this catches a regression where one of
// the override branches was unconditionally applied.
func TestNewPool_LiveSuccess(t *testing.T) {
	t.Parallel()
	dsn := testutil.StartPostgres(t)
	pool, err := storage.NewPool(context.Background(), storage.PoolConfig{
		DatabaseURL:     dsn,
		MaxConns:        3,
		MinConns:        1,
		MaxConnLifetime: 30 * time.Second,
		MaxConnIdleTime: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	if got := pool.Config().MaxConns; got != 3 {
		t.Errorf("MaxConns = %d, want 3", got)
	}
	if got := pool.Config().MinConns; got != 1 {
		t.Errorf("MinConns = %d, want 1", got)
	}
	if got := pool.Config().MaxConnLifetime; got != 30*time.Second {
		t.Errorf("MaxConnLifetime = %v, want 30s", got)
	}
	if got := pool.Config().MaxConnIdleTime; got != 15*time.Second {
		t.Errorf("MaxConnIdleTime = %v, want 15s", got)
	}
}
