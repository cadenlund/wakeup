package passwordreset_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/repository/passwordreset"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via direct SQL. The user repository's MakeUser
// fixture isn't reachable from here without a circular import; raw SQL is
// fine for fixture data.
func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u_"+id.String()[:8], id.String()+"@x.test")
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

func hash(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func TestCreate_Get_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := passwordreset.New(pool)

	uid := makeUser(ctx, t, pool)
	tokenHash := hash("token-1")
	expires := time.Now().UTC().Add(time.Hour)

	if err := repo.Create(ctx, tokenHash, uid, expires); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, tokenHash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != uid {
		t.Errorf("UserID mismatch")
	}
}

func TestGet_ExpiredReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := passwordreset.New(pool)

	uid := makeUser(ctx, t, pool)
	h := hash("expired")
	if err := repo.Create(ctx, h, uid, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Get(ctx, h); !errors.Is(err, passwordreset.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMarkUsed_GetAfterReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := passwordreset.New(pool)

	uid := makeUser(ctx, t, pool)
	h := hash("once")
	if err := repo.Create(ctx, h, uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkUsed(ctx, h); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	if _, err := repo.Get(ctx, h); !errors.Is(err, passwordreset.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after MarkUsed, got %v", err)
	}
	// Marking again returns ErrNotFound (already consumed).
	if err := repo.MarkUsed(ctx, h); !errors.Is(err, passwordreset.ErrNotFound) {
		t.Fatalf("second MarkUsed: expected ErrNotFound, got %v", err)
	}
}

func TestDeleteExpiredAndUsed_RemovesBoth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := passwordreset.New(pool)

	uid := makeUser(ctx, t, pool)
	expired := hash("expired")
	used := hash("used")
	keeper := hash("live")

	must := func(err error) {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	must(repo.Create(ctx, expired, uid, time.Now().UTC().Add(-time.Hour)))
	must(repo.Create(ctx, used, uid, time.Now().UTC().Add(time.Hour)))
	must(repo.MarkUsed(ctx, used))
	must(repo.Create(ctx, keeper, uid, time.Now().UTC().Add(time.Hour)))

	n, err := repo.DeleteExpiredAndUsed(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredAndUsed: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows deleted, got %d", n)
	}
	// Live row still present.
	if _, err := repo.Get(ctx, keeper); err != nil {
		t.Errorf("live row should still be reachable: %v", err)
	}
}

// WithTx returns a Queries bound to a tx; the returned instance reads
// its own writes and a Rollback drops them.
func TestWithTx_RollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := passwordreset.New(pool)
	uid := makeUser(ctx, t, pool)
	tokenHash := hash("tx-token")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	txRepo := repo.WithTx(tx)
	if err := txRepo.Create(ctx, tokenHash, uid, time.Now().UTC().Add(time.Hour)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Create in tx: %v", err)
	}
	// Read inside the tx — proves the binding is genuinely tx-scoped.
	if _, err := txRepo.Get(ctx, tokenHash); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Get inside tx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := repo.Get(ctx, tokenHash); !errors.Is(err, passwordreset.ErrNotFound) {
		t.Errorf("after Rollback, Get = %v, want ErrNotFound", err)
	}
}

// Closed-pool sweep — every public method's wrapped error path
// surfaces a non-nil error.
func TestRepo_DBClosedErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := passwordreset.New(pool)
	uid := makeUser(ctx, t, pool)
	pool.Close()

	tokenHash := hash("closed-token")
	expires := time.Now().UTC().Add(time.Hour)
	if err := repo.Create(ctx, tokenHash, uid, expires); err == nil {
		t.Error("Create: want error")
	}
	if _, err := repo.Get(ctx, tokenHash); err == nil {
		t.Error("Get: want error")
	} else if errors.Is(err, passwordreset.ErrNotFound) {
		t.Errorf("Get returned ErrNotFound on closed pool: %v", err)
	}
	if err := repo.MarkUsed(ctx, tokenHash); err == nil {
		t.Error("MarkUsed: want error")
	}
	if _, err := repo.DeleteExpiredAndUsed(ctx); err == nil {
		t.Error("DeleteExpiredAndUsed: want error")
	}
}
