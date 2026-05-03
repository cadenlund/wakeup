package friend_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	friendrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/friend"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

type stack struct {
	svc     *friend.Service
	users   *userrepo.Queries
	friends *friendrepo.Queries
	pool    *pgxpool.Pool
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	users := userrepo.New(pool)
	friends := friendrepo.New(pool)
	svc, err := friend.New(friend.Config{Friends: friends, Users: users})
	if err != nil {
		t.Fatalf("friend.New: %v", err)
	}
	return &stack{svc: svc, users: users, friends: friends, pool: pool}
}

func makeUser(ctx context.Context, t *testing.T, st *stack) domain.User {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	created, err := st.users.Create(ctx, userrepo.CreateParams{
		ID: id, Username: "u" + full, DisplayName: "T",
		Email: full + "@x.test", PasswordHash: "h",
	})
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return created
}

// ListAcceptedFriendIDs is the §11.3 / §9.2 fan-out helper used by
// the presence service to enumerate "who do I notify when I change
// status." Unit-tested here so the §13.8 audit isn't skipping it.

func TestListAcceptedFriendIDs_ReturnsAcceptedOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	owner := makeUser(ctx, t, st)
	friend1 := makeUser(ctx, t, st)
	friend2 := makeUser(ctx, t, st)
	pendingTarget := makeUser(ctx, t, st)

	// owner ↔ friend1 + owner ↔ friend2 accepted.
	for _, target := range []domain.User{friend1, friend2} {
		f, err := st.svc.SendRequest(ctx, owner.ID, target.Username)
		if err != nil {
			t.Fatalf("SendRequest %s: %v", target.Username, err)
		}
		if _, err := st.svc.AcceptRequest(ctx, target.ID, f.ID); err != nil {
			t.Fatalf("AcceptRequest %s: %v", target.Username, err)
		}
	}
	// owner ↔ pendingTarget pending (must NOT appear in the list).
	if _, err := st.svc.SendRequest(ctx, owner.ID, pendingTarget.Username); err != nil {
		t.Fatalf("SendRequest pending: %v", err)
	}

	got, err := st.svc.ListAcceptedFriendIDs(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ListAcceptedFriendIDs: %v", err)
	}
	// Build a set so the test fails on duplicates as well as on
	// missing/extra IDs — len(got) alone wouldn't catch a regression
	// that returned `[friend1, friend1]` for two distinct friendships.
	gotSet := make(map[uuid.UUID]int, len(got))
	for _, id := range got {
		gotSet[id]++
	}
	for id, n := range gotSet {
		if n != 1 {
			t.Errorf("friend id %v returned %d times (duplicate)", id, n)
		}
	}
	want := map[uuid.UUID]struct{}{friend1.ID: {}, friend2.ID: {}}
	if len(gotSet) != len(want) {
		t.Fatalf("got %d distinct accepted friends, want %d: %+v", len(gotSet), len(want), got)
	}
	for id := range want {
		if _, ok := gotSet[id]; !ok {
			t.Errorf("expected friend id %v missing from result", id)
		}
	}
	if _, leaked := gotSet[pendingTarget.ID]; leaked {
		t.Errorf("pending friend leaked into accepted list: %v", pendingTarget.ID)
	}
	if _, leaked := gotSet[owner.ID]; leaked {
		t.Errorf("owner id leaked into own accepted list: %v", owner.ID)
	}
}

func TestListAcceptedFriendIDs_EmptyForLonelyUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	uid := makeUser(ctx, t, st).ID
	got, err := st.svc.ListAcceptedFriendIDs(ctx, uid)
	if err != nil {
		t.Fatalf("ListAcceptedFriendIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0, got %+v", got)
	}
}

func asAPIError(t *testing.T, err error) *apierror.Error {
	t.Helper()
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	return ae
}

// --- SendRequest --------------------------------------------------------

func TestSendRequest_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)

	got, err := st.svc.SendRequest(ctx, a.ID, b.Username)
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if got.RequesterID != a.ID || got.AddresseeID != b.ID {
		t.Errorf("direction wrong: %+v", got)
	}
	if got.Status != domain.FriendshipPending {
		t.Errorf("Status = %q, want pending", got.Status)
	}
}

func TestSendRequest_RejectsSelf(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.SendRequest(ctx, a.ID, a.Username)
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestSendRequest_TargetNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.SendRequest(ctx, a.ID, "ghost-user-doesnt-exist")
	if err == nil {
		t.Fatal("expected error")
	}
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestSendRequest_DuplicatePending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	if _, err := st.svc.SendRequest(ctx, a.ID, b.Username); err != nil {
		t.Fatalf("first SendRequest: %v", err)
	}
	_, err := st.svc.SendRequest(ctx, a.ID, b.Username)
	if err == nil {
		t.Fatal("expected conflict")
	}
	if asAPIError(t, err).Code != apierror.CodeConflict {
		t.Errorf("Code = %q, want CONFLICT", asAPIError(t, err).Code)
	}
}

func TestSendRequest_PrevBlockProducesConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	if _, err := st.svc.Block(ctx, b.ID, a.ID); err != nil {
		t.Fatalf("Block: %v", err)
	}
	// A → B is blocked by B; A's request collides on the pair-unique
	// index and surfaces as Conflict (not "blocked" per se — we don't
	// leak that B blocked A).
	_, err := st.svc.SendRequest(ctx, a.ID, b.Username)
	if err == nil {
		t.Fatal("expected conflict")
	}
	if asAPIError(t, err).Code != apierror.CodeConflict {
		t.Errorf("Code = %q, want CONFLICT", asAPIError(t, err).Code)
	}
}

// --- AcceptRequest ------------------------------------------------------

func TestAcceptRequest_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	pending, _ := st.svc.SendRequest(ctx, a.ID, b.Username)

	got, err := st.svc.AcceptRequest(ctx, b.ID, pending.ID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got.Status != domain.FriendshipAccepted {
		t.Errorf("Status = %q, want accepted", got.Status)
	}
}

func TestAcceptRequest_OnlyAddressee(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	pending, _ := st.svc.SendRequest(ctx, a.ID, b.Username)

	// Requester (a) tries to accept their own outgoing request — denied.
	_, err := st.svc.AcceptRequest(ctx, a.ID, pending.ID)
	if err == nil {
		t.Fatal("expected forbidden")
	}
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q, want FORBIDDEN", asAPIError(t, err).Code)
	}
}

func TestAcceptRequest_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.AcceptRequest(ctx, a.ID, uuid.New())
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestAcceptRequest_AlreadyAccepted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	pending, _ := st.svc.SendRequest(ctx, a.ID, b.Username)
	if _, err := st.svc.AcceptRequest(ctx, b.ID, pending.ID); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	_, err := st.svc.AcceptRequest(ctx, b.ID, pending.ID)
	if asAPIError(t, err).Code != apierror.CodeConflict {
		t.Errorf("Code = %q, want CONFLICT", asAPIError(t, err).Code)
	}
}

// --- DeclineRequest -----------------------------------------------------

func TestDeclineRequest_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	pending, _ := st.svc.SendRequest(ctx, a.ID, b.Username)

	if err := st.svc.DeclineRequest(ctx, b.ID, pending.ID); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	if _, err := st.friends.GetByID(ctx, pending.ID); !errors.Is(err, friendrepo.ErrNotFound) {
		t.Errorf("after Decline, GetByID = %v, want ErrNotFound", err)
	}
}

func TestDeclineRequest_OnlyAddressee(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	pending, _ := st.svc.SendRequest(ctx, a.ID, b.Username)

	err := st.svc.DeclineRequest(ctx, a.ID, pending.ID)
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q, want FORBIDDEN", asAPIError(t, err).Code)
	}
}

// --- ListFriends + ListRequests ----------------------------------------

func TestListFriends_AcceptedOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	me := makeUser(ctx, t, st)
	friend1 := makeUser(ctx, t, st)
	friend2 := makeUser(ctx, t, st)
	pendingFriend := makeUser(ctx, t, st)

	for _, other := range []domain.User{friend1, friend2} {
		req, _ := st.svc.SendRequest(ctx, me.ID, other.Username)
		if _, err := st.svc.AcceptRequest(ctx, other.ID, req.ID); err != nil {
			t.Fatalf("Accept: %v", err)
		}
	}
	// Pending request — must NOT show up in ListFriends.
	if _, err := st.svc.SendRequest(ctx, me.ID, pendingFriend.Username); err != nil {
		t.Fatalf("send pending: %v", err)
	}

	res, err := st.svc.ListFriends(ctx, friend.ListFriendsParams{UserID: me.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListFriends: %v", err)
	}
	if len(res.Friendships) != 2 {
		t.Errorf("len = %d, want 2", len(res.Friendships))
	}
	for _, f := range res.Friendships {
		if f.Status != domain.FriendshipAccepted {
			t.Errorf("non-accepted leaked: %+v", f)
		}
	}
}

func TestListRequests_PartitionsByDirection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	me := makeUser(ctx, t, st)
	out := makeUser(ctx, t, st) // me → out
	in := makeUser(ctx, t, st)  // in → me

	if _, err := st.svc.SendRequest(ctx, me.ID, out.Username); err != nil {
		t.Fatalf("outgoing: %v", err)
	}
	if _, err := st.svc.SendRequest(ctx, in.ID, me.Username); err != nil {
		t.Fatalf("incoming: %v", err)
	}

	res, err := st.svc.ListRequests(ctx, me.ID)
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(res.Outgoing) != 1 {
		t.Errorf("Outgoing len = %d, want 1", len(res.Outgoing))
	}
	if len(res.Incoming) != 1 {
		t.Errorf("Incoming len = %d, want 1", len(res.Incoming))
	}
	if res.Outgoing[0].AddresseeID != out.ID {
		t.Errorf("outgoing target = %s, want %s", res.Outgoing[0].AddresseeID, out.ID)
	}
	if res.Incoming[0].RequesterID != in.ID {
		t.Errorf("incoming requester = %s, want %s", res.Incoming[0].RequesterID, in.ID)
	}
}

// --- Unfriend ----------------------------------------------------------

func TestUnfriend_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	pending, _ := st.svc.SendRequest(ctx, a.ID, b.Username)
	_, _ = st.svc.AcceptRequest(ctx, b.ID, pending.ID)

	if err := st.svc.Unfriend(ctx, a.ID, b.ID); err != nil {
		t.Fatalf("Unfriend: %v", err)
	}
	if _, err := st.friends.GetByPair(ctx, a.ID, b.ID); !errors.Is(err, friendrepo.ErrNotFound) {
		t.Errorf("after Unfriend, GetByPair = %v, want ErrNotFound", err)
	}
}

func TestUnfriend_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	err := st.svc.Unfriend(ctx, a.ID, b.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestUnfriend_PendingIsConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	if _, err := st.svc.SendRequest(ctx, a.ID, b.Username); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	err := st.svc.Unfriend(ctx, a.ID, b.ID)
	if asAPIError(t, err).Code != apierror.CodeConflict {
		t.Errorf("Code = %q, want CONFLICT", asAPIError(t, err).Code)
	}
}

// --- Block / Unblock ---------------------------------------------------

func TestBlock_FromNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)

	got, err := st.svc.Block(ctx, a.ID, b.ID)
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	if got.RequesterID != a.ID {
		t.Errorf("RequesterID = %s, want %s (the blocker)", got.RequesterID, a.ID)
	}
	if got.Status != domain.FriendshipBlocked {
		t.Errorf("Status = %q, want blocked", got.Status)
	}
}

func TestBlock_ReplacesPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	if _, err := st.svc.SendRequest(ctx, a.ID, b.Username); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if _, err := st.svc.Block(ctx, a.ID, b.ID); err != nil {
		t.Fatalf("Block: %v", err)
	}
	got, err := st.friends.GetByPair(ctx, a.ID, b.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}
	if got.Status != domain.FriendshipBlocked {
		t.Errorf("Status = %q, want blocked", got.Status)
	}
}

func TestBlock_RejectsSelf(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.Block(ctx, a.ID, a.ID)
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestBlock_TargetMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.Block(ctx, a.ID, uuid.New())
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestBlock_DoesNotOverwriteOtherPartyBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	// b blocks a first.
	if _, err := st.svc.Block(ctx, b.ID, a.ID); err != nil {
		t.Fatalf("b Block: %v", err)
	}
	// a's attempt to block must NOT overwrite b's block — they get
	// Forbidden (no leak) and the existing blocked row stays.
	_, err := st.svc.Block(ctx, a.ID, b.ID)
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q, want FORBIDDEN", asAPIError(t, err).Code)
	}
	got, err := st.friends.GetByPair(ctx, a.ID, b.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}
	if got.RequesterID != b.ID {
		t.Errorf("blocker overwritten: requester = %s, want %s", got.RequesterID, b.ID)
	}
}

func TestUnblock_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	if _, err := st.svc.Block(ctx, a.ID, b.ID); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if err := st.svc.Unblock(ctx, a.ID, b.ID); err != nil {
		t.Fatalf("Unblock: %v", err)
	}
	if _, err := st.friends.GetByPair(ctx, a.ID, b.ID); !errors.Is(err, friendrepo.ErrNotFound) {
		t.Errorf("after Unblock, GetByPair = %v, want ErrNotFound", err)
	}
}

func TestUnblock_OnlyBlockerCanCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	// b blocked a; a tries to unblock — they shouldn't even know about it.
	if _, err := st.svc.Block(ctx, b.ID, a.ID); err != nil {
		t.Fatalf("Block: %v", err)
	}
	err := st.svc.Unblock(ctx, a.ID, b.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestUnblock_NoRowReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	err := st.svc.Unblock(ctx, a.ID, b.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// --- New / config validation -------------------------------------------

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := friend.New(friend.Config{}); err == nil {
		t.Error("nil deps should error")
	}
}
