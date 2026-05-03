package middleware_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/middleware"
)

// fakeSweeperRepo records DeleteExpired calls + can be programmed to
// return an error or a row count.
type fakeSweeperRepo struct {
	calls atomic.Int64
	rows  int64
	err   error
}

func (f *fakeSweeperRepo) DeleteExpired(_ context.Context) (int64, error) {
	f.calls.Add(1)
	return f.rows, f.err
}

func TestNewIdempotencySweeper_RejectsMissingRepo(t *testing.T) {
	t.Parallel()
	if _, err := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{}); err == nil {
		t.Error("expected error for nil Repo")
	}
}

// Defaults: nil Logger → slog.Default(), zero Interval → 1h.
func TestNewIdempotencySweeper_DefaultsLoggerAndInterval(t *testing.T) {
	t.Parallel()
	s, err := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{
		Repo: &fakeSweeperRepo{},
	})
	if err != nil {
		t.Fatalf("NewIdempotencySweeper: %v", err)
	}
	if s.Name() != "idempotency-sweeper" {
		t.Errorf("Name() = %q, want idempotency-sweeper", s.Name())
	}
	if got := s.Interval(); got != time.Hour {
		t.Errorf("Interval() = %v, want 1h default", got)
	}
}

// Explicit Interval is preserved.
func TestNewIdempotencySweeper_HonorsCustomInterval(t *testing.T) {
	t.Parallel()
	s, err := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{
		Repo:     &fakeSweeperRepo{},
		Interval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewIdempotencySweeper: %v", err)
	}
	if got := s.Interval(); got != 5*time.Minute {
		t.Errorf("Interval() = %v, want 5m", got)
	}
}

// Run on a row-yielding repo returns nil — exercises the
// rows-deleted log branch.
func TestSweeper_Run_LogsAndReturnsNilOnDeletes(t *testing.T) {
	t.Parallel()
	repo := &fakeSweeperRepo{rows: 7}
	s, err := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{Repo: repo})
	if err != nil {
		t.Fatalf("NewIdempotencySweeper: %v", err)
	}
	if err := s.Run(context.Background()); err != nil {
		t.Errorf("Run: %v", err)
	}
	if repo.calls.Load() != 1 {
		t.Errorf("DeleteExpired calls = %d, want 1", repo.calls.Load())
	}
}

// Zero deletes — covers the n==0 (no log) branch.
func TestSweeper_Run_NoOpReturnsNil(t *testing.T) {
	t.Parallel()
	repo := &fakeSweeperRepo{rows: 0}
	s, _ := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{Repo: repo})
	if err := s.Run(context.Background()); err != nil {
		t.Errorf("Run: %v", err)
	}
}

// Repo error propagates verbatim — runner code retries on its own
// schedule, so the sweeper itself stays loud.
func TestSweeper_Run_PropagatesError(t *testing.T) {
	t.Parallel()
	want := errors.New("db down")
	repo := &fakeSweeperRepo{err: want}
	s, _ := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{Repo: repo})
	if err := s.Run(context.Background()); !errors.Is(err, want) {
		t.Errorf("Run: got %v, want %v", err, want)
	}
}
