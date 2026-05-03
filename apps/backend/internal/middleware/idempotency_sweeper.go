package middleware

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// IdempotencySweeperRepo is the slice of the idempotency repository
// the sweeper needs. Defining it here keeps the sweeper free of a
// pgx import and matches IdempotencyStore's "small consumer-side
// interface" pattern.
type IdempotencySweeperRepo interface {
	DeleteExpired(ctx context.Context) (int64, error)
}

// IdempotencySweeperConfig packages the sweeper deps.
type IdempotencySweeperConfig struct {
	Repo     IdempotencySweeperRepo
	Logger   *slog.Logger
	Interval time.Duration // defaults to 1h per §4.8
}

// IdempotencySweeper implements job.Job for the §4.12 background tick
// that drops expired idempotency_keys rows. Living next to the
// idempotency middleware keeps both halves of the §4.8 lifecycle in
// one package — same import that sets the cache TTL also runs the GC.
type IdempotencySweeper struct {
	repo     IdempotencySweeperRepo
	logger   *slog.Logger
	interval time.Duration
}

// NewIdempotencySweeper wires the sweeper. Returns an error when Repo
// is nil; logger and interval default to slog.Default() and 1h.
func NewIdempotencySweeper(cfg IdempotencySweeperConfig) (*IdempotencySweeper, error) {
	if cfg.Repo == nil {
		return nil, errors.New("middleware: IdempotencySweeperConfig.Repo is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	return &IdempotencySweeper{repo: cfg.Repo, logger: logger, interval: interval}, nil
}

// Name implements job.Job.
func (*IdempotencySweeper) Name() string { return "idempotency-sweeper" }

// Interval implements job.Job.
func (s *IdempotencySweeper) Interval() time.Duration { return s.interval }

// Run deletes every expired row and logs the count.
func (s *IdempotencySweeper) Run(ctx context.Context) error {
	n, err := s.repo.DeleteExpired(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		s.logger.InfoContext(ctx, "idempotency sweeper: rows deleted",
			slog.Int64("count", n),
		)
	}
	return nil
}
