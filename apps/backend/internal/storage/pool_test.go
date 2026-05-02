package storage_test

import (
	"context"
	"errors"
	"testing"

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
