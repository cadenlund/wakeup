package notificationpref_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/repository/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL so the FK from notification_preferences
// is valid. Avoids the import cycle the user repository's fixture would
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

func TestGetOrCreate_DefaultsAllTrueOnFirstCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)
	uid := makeUser(ctx, t, pool)

	got, err := repo.GetOrCreate(ctx, uid)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if got.UserID != uid {
		t.Errorf("UserID mismatch")
	}
	if !got.DirectMessages || !got.GroupMessages || !got.FriendRequests || !got.Calls {
		t.Errorf("expected all defaults true, got %+v", got)
	}
}

func TestGetOrCreate_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)
	uid := makeUser(ctx, t, pool)

	first, err := repo.GetOrCreate(ctx, uid)
	if err != nil {
		t.Fatalf("first GetOrCreate: %v", err)
	}

	// Mutate the row, then re-call GetOrCreate. Because of the
	// ON CONFLICT DO UPDATE SET user_id = EXCLUDED.user_id no-op,
	// existing booleans MUST survive. (Setting user_id to itself doesn't
	// trip the updated_at trigger because the new value equals the old.)
	off := false
	if _, err := repo.Patch(ctx, notificationpref.PatchParams{
		UserID: uid, DirectMessages: &off,
	}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	second, err := repo.GetOrCreate(ctx, uid)
	if err != nil {
		t.Fatalf("second GetOrCreate: %v", err)
	}
	if second.DirectMessages {
		t.Fatalf("second GetOrCreate clobbered patched value: %+v", second)
	}
	// First call's row should also be intact (we only re-checked via the
	// second variable, but the first scanned the inserted row).
	if first.UserID != uid {
		t.Errorf("first call returned wrong UserID: %v", first.UserID)
	}
}

func TestGet_NotFoundForFreshUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.Get(ctx, uid); !errors.Is(err, notificationpref.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for user with no row, got %v", err)
	}
}

func TestGet_ReturnsRowAfterCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.GetOrCreate(ctx, uid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := repo.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != uid {
		t.Errorf("UserID mismatch: %v", got.UserID)
	}
	if !got.DirectMessages || !got.GroupMessages || !got.FriendRequests || !got.Calls {
		t.Errorf("expected all defaults true, got %+v", got)
	}
}

func TestGet_ReflectsPatchedValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.GetOrCreate(ctx, uid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	off := false
	if _, err := repo.Patch(ctx, notificationpref.PatchParams{
		UserID: uid, Calls: &off,
	}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := repo.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Calls {
		t.Errorf("Get should reflect patched Calls=false")
	}
}

func TestPatch_PartialPreservesUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.GetOrCreate(ctx, uid); err != nil {
		t.Fatalf("seed: %v", err)
	}

	off := false
	got, err := repo.Patch(ctx, notificationpref.PatchParams{
		UserID:         uid,
		FriendRequests: &off,
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if got.FriendRequests != false {
		t.Errorf("FriendRequests = true, want false")
	}
	// Untouched fields keep their (true) defaults.
	if !got.DirectMessages || !got.GroupMessages || !got.Calls {
		t.Errorf("untouched fields changed: %+v", got)
	}
}

func TestPatch_AllFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.GetOrCreate(ctx, uid); err != nil {
		t.Fatalf("seed: %v", err)
	}

	off := false
	got, err := repo.Patch(ctx, notificationpref.PatchParams{
		UserID:         uid,
		DirectMessages: &off,
		GroupMessages:  &off,
		FriendRequests: &off,
		Calls:          &off,
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if got.DirectMessages || got.GroupMessages || got.FriendRequests || got.Calls {
		t.Errorf("expected all false, got %+v", got)
	}
}

func TestPatch_OfMissingRowReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)

	off := false
	_, err := repo.Patch(ctx, notificationpref.PatchParams{
		UserID: uuid.New(), DirectMessages: &off,
	})
	if !errors.Is(err, notificationpref.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCascadeDeleteWithUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := notificationpref.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.GetOrCreate(ctx, uid); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Hard-delete the user (the FK is ON DELETE CASCADE per migration 0012).
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", uid); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM notification_preferences WHERE user_id = $1", uid,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("ON DELETE CASCADE didn't fire: %d rows remain", count)
	}
}
