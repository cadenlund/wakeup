package devicetoken_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/devicetoken"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL so the FK from device_tokens is
// valid. Avoids the import cycle the user repository's fixture would
// introduce.
func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u"+full, full+"@x.test")
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

func TestRegister_InsertsNewRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)

	got, err := repo.Register(ctx, uid, "ExponentPushToken[abc]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got.ID == uuid.Nil {
		t.Errorf("ID should be assigned")
	}
	if got.UserID != uid {
		t.Errorf("UserID = %v, want %v", got.UserID, uid)
	}
	if got.ExpoToken != "ExponentPushToken[abc]" {
		t.Errorf("ExpoToken = %q", got.ExpoToken)
	}
	if got.Platform != domain.DeviceIOS {
		t.Errorf("Platform = %q, want ios", got.Platform)
	}
	if got.CreatedAt.IsZero() || got.LastSeenAt.IsZero() {
		t.Errorf("timestamps should be set: %+v", got)
	}
}

func TestRegister_SamePairUpdatesLastSeenAndPlatform(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)

	first, err := repo.Register(ctx, uid, "ExponentPushToken[same]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	// Force last_seen_at into the past so the update bumps it forward.
	if _, err := pool.Exec(ctx,
		"UPDATE device_tokens SET last_seen_at = $1 WHERE id = $2",
		time.Now().UTC().Add(-time.Hour), first.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	second, err := repo.Register(ctx, uid, "ExponentPushToken[same]", domain.DeviceAndroid)
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("expected stable id on re-register: first=%v second=%v", first.ID, second.ID)
	}
	if second.Platform != domain.DeviceAndroid {
		t.Errorf("Platform should refresh to android, got %q", second.Platform)
	}
	// We backdated to first.LastSeenAt - 1h before the second Register, so
	// the postgres-side now() that fired during the upsert MUST yield a
	// timestamp strictly after first.LastSeenAt.
	if !second.LastSeenAt.After(first.LastSeenAt) {
		t.Errorf("LastSeenAt should bump strictly forward: first=%v second=%v",
			first.LastSeenAt, second.LastSeenAt)
	}

	// Only one row should exist.
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM device_tokens WHERE user_id = $1", uid,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after re-register, got %d", count)
	}
}

func TestRegister_DifferentTokensCoexist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)

	a, err := repo.Register(ctx, uid, "ExponentPushToken[a]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("Register a: %v", err)
	}
	b, err := repo.Register(ctx, uid, "ExponentPushToken[b]", domain.DeviceAndroid)
	if err != nil {
		t.Fatalf("Register b: %v", err)
	}
	if a.ID == b.ID {
		t.Errorf("distinct tokens should yield distinct ids")
	}
}

func TestDelete_ScopedToUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	owner := makeUser(ctx, t, pool)
	thief := makeUser(ctx, t, pool)

	tok, err := repo.Register(ctx, owner, "ExponentPushToken[del]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Wrong user: no rows touched, ErrNotFound.
	if err := repo.Delete(ctx, tok.ID, thief); !errors.Is(err, devicetoken.ErrNotFound) {
		t.Fatalf("Delete by thief: expected ErrNotFound, got %v", err)
	}
	// Token still present.
	list, err := repo.ListByUser(ctx, owner)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected token to survive thief delete, got %d rows", len(list))
	}

	// Right user: deletes.
	if err := repo.Delete(ctx, tok.ID, owner); err != nil {
		t.Fatalf("Delete by owner: %v", err)
	}
	list, err = repo.ListByUser(ctx, owner)
	if err != nil {
		t.Fatalf("ListByUser after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", len(list))
	}
}

func TestDelete_MissingReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)

	if err := repo.Delete(ctx, uuid.New(), uid); !errors.Is(err, devicetoken.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListByUser_OrdersByLastSeenDesc(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)

	older, err := repo.Register(ctx, uid, "ExponentPushToken[older]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("Register older: %v", err)
	}
	// Backdate older so the ordering is deterministic.
	if _, err := pool.Exec(ctx,
		"UPDATE device_tokens SET last_seen_at = $1 WHERE id = $2",
		time.Now().UTC().Add(-time.Hour), older.ID,
	); err != nil {
		t.Fatalf("backdate older: %v", err)
	}
	newer, err := repo.Register(ctx, uid, "ExponentPushToken[newer]", domain.DeviceAndroid)
	if err != nil {
		t.Fatalf("Register newer: %v", err)
	}

	list, err := repo.ListByUser(ctx, uid)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(list))
	}
	if list[0].ID != newer.ID {
		t.Errorf("expected newest first; got %v then %v", list[0].ID, list[1].ID)
	}
}

func TestListByUser_EmptyReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)

	list, err := repo.ListByUser(ctx, uid)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if list == nil {
		t.Errorf("expected non-nil empty slice for ranging")
	}
	if len(list) != 0 {
		t.Errorf("expected 0 rows, got %d", len(list))
	}
}

func TestListByUser_ScopedToUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)

	if _, err := repo.Register(ctx, a, "ExponentPushToken[a]", domain.DeviceIOS); err != nil {
		t.Fatalf("Register a: %v", err)
	}
	if _, err := repo.Register(ctx, b, "ExponentPushToken[b]", domain.DeviceAndroid); err != nil {
		t.Fatalf("Register b: %v", err)
	}

	listA, err := repo.ListByUser(ctx, a)
	if err != nil {
		t.Fatalf("ListByUser a: %v", err)
	}
	if len(listA) != 1 || listA[0].UserID != a {
		t.Errorf("a's list leaked rows: %+v", listA)
	}
}

func TestCascadeDeleteWithUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.Register(ctx, uid, "ExponentPushToken[bye]", domain.DeviceIOS); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Hard-delete the user (the FK is ON DELETE CASCADE per migration 0009).
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", uid); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM device_tokens WHERE user_id = $1", uid,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("ON DELETE CASCADE didn't fire: %d rows remain", count)
	}
}

// WithTx returns a Queries bound to a tx; reads inside the tx see
// uncommitted writes, and a Rollback drops them.
func TestWithTx_RollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	txRepo := repo.WithTx(tx)
	_, err = txRepo.Register(ctx, uid, "ExponentPushToken[tx]", domain.DeviceIOS)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Register in tx: %v", err)
	}
	rows, err := txRepo.ListByUser(ctx, uid)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("ListByUser inside tx: %v", err)
	}
	if len(rows) != 1 {
		_ = tx.Rollback(ctx)
		t.Errorf("ListByUser in tx returned %d, want 1", len(rows))
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	post, err := repo.ListByUser(ctx, uid)
	if err != nil {
		t.Fatalf("ListByUser post-rollback: %v", err)
	}
	if len(post) != 0 {
		t.Errorf("after Rollback, ListByUser = %d, want 0", len(post))
	}
}

// Closed-pool sweep — every public method surfaces the wrapped error.
func TestRepo_DBClosedErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := devicetoken.New(pool)
	uid := makeUser(ctx, t, pool)
	tok, err := repo.Register(ctx, uid, "ExponentPushToken[seed]", domain.DeviceIOS)
	if err != nil {
		t.Fatalf("seed Register: %v", err)
	}
	pool.Close()

	if _, err := repo.Register(ctx, uid, "x", domain.DeviceIOS); err == nil {
		t.Error("Register: want error")
	}
	if err := repo.Delete(ctx, tok.ID, uid); err == nil {
		t.Error("Delete: want error")
	}
	if _, err := repo.ListByUser(ctx, uid); err == nil {
		t.Error("ListByUser: want error")
	}
}
