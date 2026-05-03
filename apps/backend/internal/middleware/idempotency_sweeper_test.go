package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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

// Run on a row-yielding repo returns nil and emits the
// rows-deleted log line. Capturing the log output catches a
// regression that drops the InfoContext call.
func TestSweeper_Run_LogsAndReturnsNilOnDeletes(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	repo := &fakeSweeperRepo{rows: 7}
	s, err := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{
		Repo:   repo,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewIdempotencySweeper: %v", err)
	}
	if err := s.Run(context.Background()); err != nil {
		t.Errorf("Run: %v", err)
	}
	if repo.calls.Load() != 1 {
		t.Errorf("DeleteExpired calls = %d, want 1", repo.calls.Load())
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"count":7`)) {
		t.Errorf("expected delete-count log with count=7, got %s", buf.String())
	}
}

// Zero deletes — covers the n==0 (no log) branch. Captures the log
// buffer as well so the negative assertion (NO log line emitted) is
// observable, not just implied.
func TestSweeper_Run_NoOpReturnsNil(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	repo := &fakeSweeperRepo{rows: 0}
	s, err := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{
		Repo:   repo,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewIdempotencySweeper: %v", err)
	}
	if err := s.Run(context.Background()); err != nil {
		t.Errorf("Run: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("zero-deletes path should not log, got %s", buf.String())
	}
}

// Repo error propagates verbatim — runner code retries on its own
// schedule, so the sweeper itself stays loud.
func TestSweeper_Run_PropagatesError(t *testing.T) {
	t.Parallel()
	want := errors.New("db down")
	repo := &fakeSweeperRepo{err: want}
	s, err := middleware.NewIdempotencySweeper(middleware.IdempotencySweeperConfig{Repo: repo})
	if err != nil {
		t.Fatalf("NewIdempotencySweeper: %v", err)
	}
	if err := s.Run(context.Background()); !errors.Is(err, want) {
		t.Errorf("Run: got %v, want %v", err, want)
	}
}
