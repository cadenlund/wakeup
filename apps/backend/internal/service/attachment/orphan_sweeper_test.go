package attachment_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	attrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/attachment"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/attachment"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// fakeStorage is a hand-rolled stand-in for the orphan sweeper's
// objectstore-shaped dependency. We track which keys have been deleted
// and, optionally, return a configurable error per key so we can
// exercise the "S3 fails → DB row stays" branch.
type fakeStorage struct {
	deleted map[string]int
	failOn  map[string]error
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{deleted: map[string]int{}, failOn: map[string]error{}}
}

func (f *fakeStorage) Delete(_ context.Context, key string) error {
	if err, ok := f.failOn[key]; ok && err != nil {
		return err
	}
	f.deleted[key]++
	return nil
}

// makeUserSweeper duplicates the makeUser helper in attachment_test.go
// because Go won't let us cross-test-file-import unexported helpers.
func makeUserSweeper(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u"+full, full+"@x.test")
	if err != nil {
		t.Fatalf("makeUserSweeper: %v", err)
	}
	return id
}

// makeOrphan creates an attachment row directly via the repo (no S3
// write) so the sweeper has rows to scan. Optionally backdates the
// row's created_at so the §9.6 24h cutoff is exceeded.
func makeOrphan(ctx context.Context, t *testing.T, pool *pgxpool.Pool, repo *attrepo.Queries, uploader uuid.UUID, age time.Duration) uuid.UUID {
	t.Helper()
	a, err := repo.Create(ctx, attrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), UploaderID: uploader,
		StorageKey:  "attachments/" + uuid.Must(uuid.NewV7()).String(),
		Filename:    "file.bin",
		ContentType: "application/octet-stream",
		SizeBytes:   12,
	})
	if err != nil {
		t.Fatalf("makeOrphan: %v", err)
	}
	if age > 0 {
		if _, err := pool.Exec(ctx,
			`UPDATE attachments SET created_at = $2 WHERE id = $1`,
			a.ID, time.Now().Add(-age),
		); err != nil {
			t.Fatalf("makeOrphan: backdate: %v", err)
		}
	}
	return a.ID
}

func newSweeper(t *testing.T, repo *attrepo.Queries, store *fakeStorage) *attachment.OrphanSweeper {
	t.Helper()
	s, err := attachment.NewOrphanSweeper(attachment.OrphanSweeperConfig{
		Repo: repo, Storage: store,
	})
	if err != nil {
		t.Fatalf("NewOrphanSweeper: %v", err)
	}
	return s
}

// --- Run --------------------------------------------------------------

func TestOrphanSweeper_NotDeletedBeforeCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attrepo.New(pool)
	store := newFakeStorage()
	uploader := makeUserSweeper(ctx, t, pool)

	// Fresh row (no backdate) — cutoff is 24h, age is 0.
	id := makeOrphan(ctx, t, pool, repo, uploader, 0)

	if err := newSweeper(t, repo, store).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Errorf("storage.Delete should not have fired: %+v", store.deleted)
	}
	if _, err := repo.GetByID(ctx, id); err != nil {
		t.Errorf("row should still exist: %v", err)
	}
}

func TestOrphanSweeper_DeletesAfterCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attrepo.New(pool)
	store := newFakeStorage()
	uploader := makeUserSweeper(ctx, t, pool)

	// Backdate beyond the 24h grace.
	id := makeOrphan(ctx, t, pool, repo, uploader, 25*time.Hour)

	if err := newSweeper(t, repo, store).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(store.deleted); got != 1 {
		t.Errorf("storage.Delete fired %d times, want 1", got)
	}
	if _, err := repo.GetByID(ctx, id); !errors.Is(err, attrepo.ErrNotFound) {
		t.Errorf("row should be deleted: %v", err)
	}
}

// S3 fails → DB row stays. Sweeper logs and moves on; next tick will
// re-attempt because the row will still appear in ListOrphansOlderThan.
func TestOrphanSweeper_S3FailureLeavesDBRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attrepo.New(pool)
	store := newFakeStorage()
	uploader := makeUserSweeper(ctx, t, pool)

	id := makeOrphan(ctx, t, pool, repo, uploader, 25*time.Hour)
	row, _ := repo.GetByID(ctx, id)
	store.failOn[row.StorageKey] = errors.New("simulated S3 outage")

	if err := newSweeper(t, repo, store).Run(ctx); err != nil {
		t.Fatalf("Run should succeed even when S3 fails on one row: %v", err)
	}
	if got := len(store.deleted); got != 0 {
		t.Errorf("no rows should have been Delete'd: %+v", store.deleted)
	}
	if _, err := repo.GetByID(ctx, id); err != nil {
		t.Errorf("DB row should remain so next tick can retry: %v", err)
	}
}

// Mixed batch: one S3 succeeds, another S3 fails. Only the succeeding
// one should be DB-deleted; the failing one stays for the next tick.
func TestOrphanSweeper_MixedBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attrepo.New(pool)
	store := newFakeStorage()
	uploader := makeUserSweeper(ctx, t, pool)

	good := makeOrphan(ctx, t, pool, repo, uploader, 25*time.Hour)
	bad := makeOrphan(ctx, t, pool, repo, uploader, 25*time.Hour)

	badRow, _ := repo.GetByID(ctx, bad)
	store.failOn[badRow.StorageKey] = errors.New("simulated")

	if err := newSweeper(t, repo, store).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := repo.GetByID(ctx, good); !errors.Is(err, attrepo.ErrNotFound) {
		t.Errorf("good row should be gone: %v", err)
	}
	if _, err := repo.GetByID(ctx, bad); err != nil {
		t.Errorf("bad row should remain: %v", err)
	}
}

// Empty list: Run must succeed without calling Delete or DeleteByIDs.
func TestOrphanSweeper_EmptyListNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attrepo.New(pool)
	store := newFakeStorage()

	if err := newSweeper(t, repo, store).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Errorf("storage.Delete should not fire: %+v", store.deleted)
	}
}

// --- Config -----------------------------------------------------------

func TestNewOrphanSweeper_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := attachment.NewOrphanSweeper(attachment.OrphanSweeperConfig{}); err == nil {
		t.Error("nil deps should error")
	}
}

func TestOrphanSweeper_NameAndInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attrepo.New(pool)
	store := newFakeStorage()
	s, err := attachment.NewOrphanSweeper(attachment.OrphanSweeperConfig{
		Repo: repo, Storage: store, Interval: 2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewOrphanSweeper: %v", err)
	}
	if s.Name() != "attachment-orphan-sweeper" {
		t.Errorf("Name = %q", s.Name())
	}
	if s.Interval() != 2*time.Hour {
		t.Errorf("Interval = %v, want 2h", s.Interval())
	}
	_ = ctx
}
