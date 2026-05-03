package attachment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	attrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/attachment"
)

// orphanCutoff is the §9.6 grace window: a row younger than 24h is kept
// even when unlinked, since a user might still be in the middle of
// composing a message that will eventually link it.
const orphanCutoff = 24 * time.Hour

// orphanInterval is the §16 7.4 sweep cadence.
const orphanInterval = time.Hour

// OrphanSweeper implements job.Job. It walks attachments older than 24h
// with no `message_attachments` row, deletes the S3 object first, then
// the DB row. Ordering matters:
//
//   - S3 first: a successful S3 delete is durable from the user's
//     perspective. If the DB delete then fails we leave the row, so the
//     next tick re-attempts (DeleteByIDs is a no-op for already-deleted
//     rows; the storage Delete is idempotent on missing keys).
//
//   - DB only after S3 succeeds: avoids the inverse window where the
//     row is gone but the bytes linger orphaned forever.
//
// Linked-after-list race: between ListOrphansOlderThan and DeleteByIDs,
// a slow client may finish composing and link the attachment. The
// repo's DeleteByIDs has a NOT EXISTS guard (PR #42) so a now-linked
// row is left alone — the worst case is the S3 object is gone but the
// row points at a nonexistent key. The §9.3 download path handles
// missing objects by just letting the presigned URL 404 the client.
type OrphanSweeper struct {
	repo    *attrepo.Queries
	storage objectDeleter
	cutoff  time.Duration
	tick    time.Duration
	logger  *slog.Logger
	now     func() time.Time
}

// objectDeleter is the slice of *objectstore.Store the sweeper needs.
// Narrowing the surface keeps tests honest — a fake only has to
// implement Delete.
type objectDeleter interface {
	Delete(ctx context.Context, key string) error
}

// OrphanSweeperConfig builds the sweeper.
type OrphanSweeperConfig struct {
	Repo    *attrepo.Queries
	Storage objectDeleter
	Logger  *slog.Logger
	// Cutoff is the orphan grace window. Defaults to 24h when zero.
	Cutoff time.Duration
	// Interval is the sweep cadence (job.Job.Interval). Defaults to 1h
	// when zero.
	Interval time.Duration
	// Now lets tests inject a fake clock. Defaults to time.Now.
	Now func() time.Time
}

// NewOrphanSweeper builds the sweeper. Repo + Storage are required.
func NewOrphanSweeper(cfg OrphanSweeperConfig) (*OrphanSweeper, error) {
	if cfg.Repo == nil {
		return nil, errors.New("attachment: OrphanSweeper requires non-nil repo")
	}
	if cfg.Storage == nil {
		return nil, errors.New("attachment: OrphanSweeper requires non-nil storage")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	cutoff := cfg.Cutoff
	if cutoff <= 0 {
		cutoff = orphanCutoff
	}
	tick := cfg.Interval
	if tick <= 0 {
		tick = orphanInterval
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &OrphanSweeper{
		repo: cfg.Repo, storage: cfg.Storage, cutoff: cutoff,
		tick: tick, logger: logger, now: now,
	}, nil
}

// Name implements job.Job.
func (s *OrphanSweeper) Name() string { return "attachment-orphan-sweeper" }

// Interval implements job.Job.
func (s *OrphanSweeper) Interval() time.Duration { return s.tick }

// Run implements job.Job. Walks every orphan older than `cutoff` and
// best-effort cleans them. A single bad S3 key doesn't stop the sweep —
// failures are logged and skipped so the rest of the batch progresses.
//
// The DB delete is attempted only when the S3 delete succeeded. A row
// whose S3 object failed to delete will be retried next tick (the row
// stays an orphan, ListOrphansOlderThan will list it again).
func (s *OrphanSweeper) Run(ctx context.Context) error {
	cutoff := s.now().Add(-s.cutoff)
	orphans, err := s.repo.ListOrphansOlderThan(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("orphan sweeper: list: %w", err)
	}
	if len(orphans) == 0 {
		return nil
	}
	deleted := make([]uuid.UUID, 0, len(orphans))
	for _, a := range orphans {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.storage.Delete(ctx, a.StorageKey); err != nil {
			s.logger.Warn("orphan sweeper: storage delete failed; will retry next tick",
				slog.String("attachment_id", a.ID.String()),
				slog.String("storage_key", a.StorageKey),
				slog.String("error", err.Error()),
			)
			continue
		}
		deleted = append(deleted, a.ID)
	}
	if len(deleted) == 0 {
		return nil
	}
	if err := s.repo.DeleteByIDs(ctx, deleted); err != nil {
		return fmt.Errorf("orphan sweeper: delete rows: %w", err)
	}
	s.logger.Info("orphan sweeper: tick",
		slog.Int("listed", len(orphans)),
		slog.Int("deleted", len(deleted)),
	)
	return nil
}
