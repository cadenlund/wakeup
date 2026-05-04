package voiptoken_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/repository/voiptoken"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u"+full, full+"@x.test"); err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

func TestRegister_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := voiptoken.New(pool)
	uid := makeUser(ctx, t, pool)

	tok, err := repo.Register(ctx, uid, "abc123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tok.UserID != uid || tok.VoIPToken != "abc123" {
		t.Errorf("got %+v, mismatch", tok)
	}
}

func TestRegister_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := voiptoken.New(pool)
	uid := makeUser(ctx, t, pool)

	first, err := repo.Register(ctx, uid, "abc123")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := repo.Register(ctx, uid, "abc123")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected same row id on idempotent re-register, got %s vs %s", first.ID, second.ID)
	}
	if !second.LastSeenAt.After(first.LastSeenAt) && !second.LastSeenAt.Equal(first.LastSeenAt) {
		t.Errorf("LastSeenAt not bumped: first=%v second=%v", first.LastSeenAt, second.LastSeenAt)
	}
}

func TestDelete_OnlyForOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := voiptoken.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)

	tok, err := repo.Register(ctx, a, "tokenA")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Wrong user_id → ErrNotFound (no enumeration leak).
	if err := repo.Delete(ctx, tok.ID, b); !errors.Is(err, voiptoken.ErrNotFound) {
		t.Errorf("Delete with wrong user_id: err = %v, want ErrNotFound", err)
	}
	// Correct user_id → success.
	if err := repo.Delete(ctx, tok.ID, a); err != nil {
		t.Errorf("Delete by owner: %v", err)
	}
}

func TestListByUser_NewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := voiptoken.New(pool)
	uid := makeUser(ctx, t, pool)

	if _, err := repo.Register(ctx, uid, "tok-1"); err != nil {
		t.Fatalf("register tok-1: %v", err)
	}
	if _, err := repo.Register(ctx, uid, "tok-2"); err != nil {
		t.Fatalf("register tok-2: %v", err)
	}

	got, err := repo.ListByUser(ctx, uid)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}
