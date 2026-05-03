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

func TestCreate_RejectsAcceptedStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)

	// Initial status must be pending or blocked — accepted requires the
	// pending → accepted transition via Accept() so accepted_at gets
	// stamped atomically.
	_, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipAccepted,
	})
	if err == nil {
		t.Fatal("Create with status=accepted should error")
	}
	if !strings.Contains(err.Error(), "invalid initial status") {
		t.Errorf("error = %v, want 'invalid initial status'", err)
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
	created, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

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
	created, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

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
	// Insert pending → accept → delete by reverse-direction lookup.
	// Repo enforces that initial status must be pending or blocked, so
	// we transition through Accept() instead of inserting accepted directly.
	created, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	})
	if err != nil {
		t.Fatalf("Create pending: %v", err)
	}
	if _, err := repo.Accept(ctx, created.ID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
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

// GetByID success returns the inserted row (the existing
// TestGetByID_NotFound only covered the not-found branch).
func TestGetByID_Success(t *testing.T) {
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
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID || got.RequesterID != a || got.AddresseeID != b {
		t.Errorf("got %+v, want id=%s requester=%s addressee=%s", got, created.ID, a, b)
	}
}

// ListAllAcceptedFriendIDs returns every accepted friend id without
// pagination — used by the §9 presence fan-out which can't afford to
// walk pages per state change.
func TestListAllAcceptedFriendIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	me := makeUser(ctx, t, pool)
	other := makeUser(ctx, t, pool)
	another := makeUser(ctx, t, pool)
	pending := makeUser(ctx, t, pool)

	for _, target := range []uuid.UUID{other, another} {
		id := uuid.Must(uuid.NewV7())
		if _, err := repo.Create(ctx, friendship.CreateParams{
			ID: id, RequesterID: me, AddresseeID: target, Status: domain.FriendshipPending,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := repo.Accept(ctx, id); err != nil {
			t.Fatalf("Accept: %v", err)
		}
	}
	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: me, AddresseeID: pending,
		Status: domain.FriendshipPending,
	}); err != nil {
		t.Fatalf("Create pending: %v", err)
	}

	got, err := repo.ListAllAcceptedFriendIDs(ctx, me)
	if err != nil {
		t.Fatalf("ListAllAcceptedFriendIDs: %v", err)
	}
	gotSet := make(map[uuid.UUID]struct{}, len(got))
	for _, id := range got {
		gotSet[id] = struct{}{}
	}
	if len(gotSet) != 2 {
		t.Fatalf("len = %d, want 2: %v", len(gotSet), got)
	}
	if _, ok := gotSet[other]; !ok {
		t.Errorf("missing %s", other)
	}
	if _, ok := gotSet[another]; !ok {
		t.Errorf("missing %s", another)
	}
	if _, leaked := gotSet[pending]; leaked {
		t.Errorf("pending friend leaked: %s", pending)
	}
}

// WithTx returns a Queries bound to the supplied tx; reads/writes
// through the new instance see the tx's view. Verifies that a row
// inserted inside a rolled-back tx does NOT show up after rollback.
func TestWithTx_RollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	txRepo := repo.WithTx(tx)
	created, err := txRepo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b,
		Status: domain.FriendshipPending,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Create in tx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, friendship.ErrNotFound) {
		t.Errorf("after Rollback, GetByID = %v, want ErrNotFound", err)
	}
}

// Closing the pool before the call surfaces every method's wrapped
// error path. The repo wraps with fmt.Errorf("friendship: ...: %w") —
// none should panic, all should return non-nil errors.
func TestRepo_DBClosedErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := friendship.New(pool)
	a, b := newPair(ctx, t, pool)
	pool.Close()

	expectErr := func(t *testing.T, name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: expected error against closed pool", name)
		}
	}
	if _, err := repo.Create(ctx, friendship.CreateParams{
		ID: uuid.Must(uuid.NewV7()), RequesterID: a, AddresseeID: b, Status: domain.FriendshipPending,
	}); err == nil {
		t.Error("Create: want error")
	}
	if _, err := repo.GetByID(ctx, uuid.New()); err == nil {
		t.Error("GetByID: want error")
	} else if errors.Is(err, friendship.ErrNotFound) {
		// Closed pool shouldn't masquerade as not-found.
		t.Errorf("GetByID returned ErrNotFound on closed pool: %v", err)
	}
	if _, err := repo.GetByPair(ctx, a, b); err == nil {
		t.Error("GetByPair: want error")
	}
	if _, err := repo.Accept(ctx, uuid.New()); err == nil {
		t.Error("Accept: want error")
	}
	expectErr(t, "Delete", repo.Delete(ctx, uuid.New()))
	expectErr(t, "DeleteByPair", repo.DeleteByPair(ctx, a, b))
	if _, err := repo.ListAllAcceptedFriendIDs(ctx, a); err == nil {
		t.Error("ListAllAcceptedFriendIDs: want error")
	}
	if _, err := repo.ListAcceptedByUser(ctx, a, nil, 10); err == nil {
		t.Error("ListAcceptedByUser: want error")
	}
	if _, err := repo.ListPendingByUser(ctx, a); err == nil {
		t.Error("ListPendingByUser: want error")
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
