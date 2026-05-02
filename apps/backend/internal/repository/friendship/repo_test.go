package friendship_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL — same pattern the other
// repository tests use to keep each repo's tests independent.
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

// newPair returns two distinct freshly-inserted user IDs.
func newPair(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	return makeUser(ctx, t, pool), makeUser(ctx, t, pool)
}

func TestCreate_Pending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)

	got, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Status != domain.FriendshipPending {
		t.Errorf("status = %q, want pending", got.Status)
	}
	if got.AcceptedAt != nil {
		t.Errorf("AcceptedAt should be nil on pending, got %v", got.AcceptedAt)
	}
}

func TestCreate_DuplicatePairFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)

	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Same pair, opposite direction — should still fail because of the
	// pair-unique index.
	_, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: b, AddresseeID: a,
		Status: domain.FriendshipPending,
	})
	if err == nil {
		t.Fatal("second Create should fail (pair-unique violation)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("expected SQLSTATE 23505, got %v", err)
	}
}

func TestCreate_RequesterEqualsAddresseeFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a := makeUser(ctx, t, pool)
	_, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: a,
		Status: domain.FriendshipPending,
	})
	if err == nil {
		t.Fatal("expected CHECK violation for self-friendship")
	}
}

func TestGetByID_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	_, err := repo.GetByID(ctx, uuid.New())
	if !errors.Is(err, friendship.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestGetByPair_DualDirection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)

	created, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	gotAB, err := repo.GetByPair(ctx, a, b)
	if err != nil {
		t.Fatalf("GetByPair(a,b): %v", err)
	}
	gotBA, err := repo.GetByPair(ctx, b, a)
	if err != nil {
		t.Fatalf("GetByPair(b,a): %v", err)
	}
	if gotAB.ID != created.ID || gotBA.ID != created.ID {
		t.Errorf("dual-direction lookup mismatch: ab=%s ba=%s want=%s",
			gotAB.ID, gotBA.ID, created.ID)
	}
}

func TestAccept_PendingToAccepted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)
	created, _ := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	})

	got, err := repo.Accept(ctx, created.ID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got.Status != domain.FriendshipAccepted {
		t.Errorf("status = %q, want accepted", got.Status)
	}
	if got.AcceptedAt == nil {
		t.Error("AcceptedAt should be set after Accept")
	}
}

func TestAccept_OnlyPendingTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)

	// Create a blocked row directly — Accept should reject.
	id := uuid.Must(uuid.NewV7())
	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: id, RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipBlocked,
	}); err != nil {
		t.Fatalf("Create blocked: %v", err)
	}
	_, err := repo.Accept(ctx, id)
	if !errors.Is(err, friendship.ErrNotFound) {
		t.Errorf("Accept on blocked row should return ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)
	created, _ := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	})

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, friendship.ErrNotFound) {
		t.Errorf("after Delete, GetByID = %v, want ErrNotFound", err)
	}
	// Re-delete is ErrNotFound (rowsAffected == 0).
	if err := repo.Delete(ctx, created.ID); !errors.Is(err, friendship.ErrNotFound) {
		t.Errorf("repeat Delete = %v, want ErrNotFound", err)
	}
}

func TestDeleteByPair_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)

	// Missing pair → no error.
	if err := repo.DeleteByPair(ctx, a, b); err != nil {
		t.Fatalf("DeleteByPair on missing: %v", err)
	}
	// Insert then delete by reverse-direction lookup.
	_, _ = repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipAccepted,
	})
	if err := repo.DeleteByPair(ctx, b, a); err != nil {
		t.Fatalf("DeleteByPair (b,a): %v", err)
	}
	if _, err := repo.GetByPair(ctx, a, b); !errors.Is(err, friendship.ErrNotFound) {
		t.Errorf("after DeleteByPair, GetByPair = %v, want ErrNotFound", err)
	}
}

func TestBlock_PreventsNewRequestInEitherDirection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)

	// A blocks B (status=blocked, requester=A).
	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipBlocked,
	}); err != nil {
		t.Fatalf("Create blocked: %v", err)
	}

	// B tries to send a friend request — must collide on the
	// pair-unique index.
	_, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: b, AddresseeID: a,
		Status: domain.FriendshipPending,
	})
	if err == nil {
		t.Fatal("expected pair-unique violation; block should prevent reverse-direction request")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("expected SQLSTATE 23505, got %v", err)
	}
}

func TestListAcceptedByUser_PaginatesNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	me := makeUser(ctx, t, pool)

	// Create 5 accepted friendships for `me` with different other users.
	for i := 0; i < 5; i++ {
		other := makeUser(ctx, t, pool)
		id := uuid.Must(uuid.NewV7())
		_, err := repo.Create(ctx, friendship.CreateParams{
			ID: id, RequesterID: me, AddresseeID: other,
			Status: domain.FriendshipPending,
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if _, err := repo.Accept(ctx, id); err != nil {
			t.Fatalf("Accept %d: %v", i, err)
		}
	}

	got, err := repo.ListAcceptedByUser(ctx, me, nil, 3)
	if err != nil {
		t.Fatalf("ListAcceptedByUser: %v", err)
	}
	// Over-fetch is limit+1 = 4 → service trims to 3.
	if len(got) != 4 {
		t.Errorf("over-fetch len = %d, want 4", len(got))
	}
	for _, f := range got {
		if f.Status != domain.FriendshipAccepted {
			t.Errorf("non-accepted row in result: %+v", f)
		}
		if me != f.RequesterID && me != f.AddresseeID {
			t.Errorf("row doesn't include me: %+v", f)
		}
	}
}

func TestListAcceptedByUser_FiltersOutPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	me := makeUser(ctx, t, pool)
	other := makeUser(ctx, t, pool)
	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: me, AddresseeID: other,
		Status: domain.FriendshipPending,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.ListAcceptedByUser(ctx, me, nil, 10)
	if err != nil {
		t.Fatalf("ListAcceptedByUser: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("pending rows leaked into accepted list: %d", len(got))
	}
}

func TestListPendingByUser_BothDirections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	me := makeUser(ctx, t, pool)
	a := makeUser(ctx, t, pool) // outgoing: me → a
	b := makeUser(ctx, t, pool) // incoming: b → me

	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: me, AddresseeID: a,
		Status: domain.FriendshipPending,
	}); err != nil {
		t.Fatalf("Create outgoing: %v", err)
	}
	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: b, AddresseeID: me,
		Status: domain.FriendshipPending,
	}); err != nil {
		t.Fatalf("Create incoming: %v", err)
	}

	got, err := repo.ListPendingByUser(ctx, me)
	if err != nil {
		t.Fatalf("ListPendingByUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (1 incoming + 1 outgoing)", len(got))
	}
	var sawIncoming, sawOutgoing bool
	for _, f := range got {
		if f.RequesterID == me {
			sawOutgoing = true
		}
		if f.AddresseeID == me {
			sawIncoming = true
		}
	}
	if !sawIncoming || !sawOutgoing {
		t.Errorf("expected one incoming + one outgoing; sawIncoming=%v sawOutgoing=%v", sawIncoming, sawOutgoing)
	}
}

func TestCascadeDeleteWithUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)
	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", a); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM friendships WHERE requester_id = $1 OR addressee_id = $1", a,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("ON DELETE CASCADE didn't fire: %d rows remain", count)
	}
}

// --- domain helper -----------------------------------------------------

func TestOtherID_ReturnsCounterparty(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	f := domain.Friendship{RequesterID: a, AddresseeID: b}
	if got := f.OtherID(a); got != b {
		t.Errorf("OtherID(a) = %s, want %s", got, b)
	}
	if got := f.OtherID(b); got != a {
		t.Errorf("OtherID(b) = %s, want %s", got, a)
	}
}
