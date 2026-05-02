package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
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
