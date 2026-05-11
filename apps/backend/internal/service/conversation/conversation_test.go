package conversation_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	friendrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

type stack struct {
	svc     *conversation.Service
	convs   *convrepo.Queries
	users   *userrepo.Queries
	friends *friendrepo.Queries
	pool    *pgxpool.Pool
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	convs := convrepo.New(pool)
	users := userrepo.New(pool)
	friends := friendrepo.New(pool)
	svc, err := conversation.New(conversation.Config{
		Pool: pool, Convs: convs, Users: users, Friends: friends,
	})
	if err != nil {
		t.Fatalf("conversation.New: %v", err)
	}
	return &stack{svc: svc, convs: convs, users: users, friends: friends, pool: pool}
}

// makeFriendship is a setup helper that establishes an accepted
// friendship between two users so createDirect's friends-only
// gate doesn't reject the test conversation. The repo only allows
// `pending` / `blocked` as initial statuses; we create + Accept in
// two steps to land in `accepted`.
func makeFriendship(ctx context.Context, t *testing.T, st *stack, a, b uuid.UUID) {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := st.friends.Create(ctx, friendrepo.CreateParams{
		ID: id, RequesterID: a, AddresseeID: b, Status: domain.FriendshipPending,
	}); err != nil {
		t.Fatalf("makeFriendship create: %v", err)
	}
	if _, err := st.friends.Accept(ctx, id); err != nil {
		t.Fatalf("makeFriendship accept: %v", err)
	}
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

func asAPIError(t *testing.T, err error) *apierror.Error {
	t.Helper()
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	return ae
}

func ptr[T any](v T) *T { return &v }

// mustCreate is a setup helper that fails the test immediately on a
// Create error. Tests that need a pre-existing conversation use this to
// avoid masking setup failures behind discarded errors (CodeRabbit
// caught the pattern on PR #35).
func mustCreate(ctx context.Context, t *testing.T, st *stack, p conversation.CreateParams) conversation.CreateResult {
	t.Helper()
	// Friends-only DM enforcement requires every test that sets up a
	// direct conversation to friend the pair first. mustCreate is a
	// "must succeed" helper; the friendship is part of that contract
	// so individual tests don't have to repeat the boilerplate.
	if p.Type == domain.ConversationDirect && len(p.MemberIDs) == 1 {
		makeFriendship(ctx, t, st, p.Creator, p.MemberIDs[0])
	}
	got, err := st.svc.Create(ctx, p)
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}
	return got
}

// --- Create direct -----------------------------------------------------

func TestCreate_DirectSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	makeFriendship(ctx, t, st, a.ID, b.ID)

	got, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Conversation.Type != domain.ConversationDirect {
		t.Errorf("Type = %q", got.Conversation.Type)
	}
	if len(got.Members) != 2 {
		t.Errorf("members len = %d, want 2", len(got.Members))
	}
}

func TestCreate_DirectDeduplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	makeFriendship(ctx, t, st, a.ID, b.ID)

	first, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: b.ID, MemberIDs: []uuid.UUID{a.ID},
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.Conversation.ID != second.Conversation.ID {
		t.Errorf("dedupe failed: first=%s second=%s", first.Conversation.ID, second.Conversation.ID)
	}
}

func TestCreate_DirectSelfFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{a.ID},
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestCreate_DirectMissingOther(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{uuid.New()},
	})
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestCreate_DirectWrongMemberCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID, c.ID},
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

// --- Create group ------------------------------------------------------

func TestCreate_GroupSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)

	got, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID, c.ID},
		Name:      ptr("Crew"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Conversation.Type != domain.ConversationGroup {
		t.Errorf("Type = %q", got.Conversation.Type)
	}
	if len(got.Members) != 3 {
		t.Errorf("members len = %d, want 3", len(got.Members))
	}
	// Creator should be admin; others should be member.
	for _, m := range got.Members {
		if m.UserID == a.ID && !m.IsAdmin() {
			t.Errorf("creator should be admin: %+v", m)
		}
		if m.UserID != a.ID && m.IsAdmin() {
			t.Errorf("non-creator should not be admin: %+v", m)
		}
	}
}

// TestCreate_GroupAllowsNilName regresses the change that made
// the group name optional on create — the mobile chats list
// renders unnamed groups with a "first names + N more" title
// fallback. Empty-string still rejects; only nil is accepted.
func TestCreate_GroupAllowsNilName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	got, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, // no name
	})
	if err != nil {
		t.Fatalf("expected nil-name group create to succeed, got %v", err)
	}
	if got.Conversation.Name != nil {
		t.Errorf("Name = %v, want nil", got.Conversation.Name)
	}
}

// Empty-string name is still rejected — that's a positive
// signal of "I tried to set a name and it was empty," which
// validateGroupName should treat as malformed.
func TestCreate_GroupRejectsEmptyName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	empty := ""
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: &empty,
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestCreate_GroupTooLarge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	others := make([]uuid.UUID, convrepo.MaxGroupMembers) // 25 others + creator = 26
	for i := range others {
		others[i] = makeUser(ctx, t, st).ID
	}
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: others, Name: ptr("Too Big"),
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestCreate_GroupMinMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	// Just creator (after dedup) → fewer than 2 total.
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{a.ID}, // creator dedup'd → 1 total
		Name:      ptr("Solo"),
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestCreate_GroupDedupesAndIgnoresCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	got, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID, b.ID, a.ID}, // dup b + creator
		Name:      ptr("Dedup"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(got.Members) != 2 {
		t.Errorf("members len = %d, want 2 (creator + b)", len(got.Members))
	}
}

func TestCreate_GroupMemberMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{uuid.New()},
		Name:      ptr("Ghost"),
	})
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestCreate_BogusType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: "bogus", Creator: a.ID, MemberIDs: []uuid.UUID{a.ID},
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

// --- Get + List --------------------------------------------------------

func TestGet_NonMemberReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	makeFriendship(ctx, t, st, a.ID, b.ID)
	got, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = st.svc.Get(ctx, c.ID, got.Conversation.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestGet_MemberSeesConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	got, err := st.svc.Get(ctx, a.ID, created.Conversation.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Conversation.ID != created.Conversation.ID {
		t.Errorf("ID mismatch")
	}
	if len(got.Members) != 2 {
		t.Errorf("members len = %d, want 2", len(got.Members))
	}
}

func TestList_OrdersByLastMessageAtDESC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)

	// Build 3 conversations and bump their last_message_at in order so
	// the DESC ordering is exercised — without distinct timestamps the
	// initial creation ordering would be the same and the test would
	// pass on count alone.
	convIDs := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		other := makeUser(ctx, t, st)
		c := mustCreate(ctx, t, st, conversation.CreateParams{
			Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{other.ID},
		})
		convIDs = append(convIDs, c.Conversation.ID)
	}
	// Touch each conversation's last_message_at in sequence, so the
	// most-recently-touched is conv[0], then [1], then [2]. We expect
	// List to return them in reverse insertion order (newest-touched first).
	base := time.Now().UTC().Add(1 * time.Hour) // future, to dodge createdAt
	for i := range convIDs {
		// i=0 oldest touch, i=2 newest touch.
		ts := base.Add(time.Duration(i) * time.Minute)
		if err := st.convs.TouchLastMessageAt(ctx, convIDs[i], ts); err != nil {
			t.Fatalf("Touch %d: %v", i, err)
		}
	}

	got, err := st.svc.List(ctx, conversation.ListParams{UserID: a.ID, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Conversations) != 3 {
		t.Fatalf("len = %d, want 3", len(got.Conversations))
	}
	// DESC ordering: each subsequent row's last_message_at must be ≤ the previous.
	for i := 1; i < len(got.Conversations); i++ {
		prev := got.Conversations[i-1].LastMessageAt
		curr := got.Conversations[i].LastMessageAt
		if curr.After(prev) {
			t.Errorf("rows out of DESC order at i=%d: %v > %v", i, curr, prev)
		}
	}
	// Newest-touched conversation should be first.
	if got.Conversations[0].ID != convIDs[2] {
		t.Errorf("first row = %s, want last-touched %s", got.Conversations[0].ID, convIDs[2])
	}
}

// --- Update ------------------------------------------------------------

func TestUpdate_GroupAdminCanRename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Old"),
	})
	got, err := st.svc.Update(ctx, conversation.UpdateParams{
		Actor: a.ID, ConvID: created.Conversation.ID, Name: ptr("New"),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name == nil || *got.Name != "New" {
		t.Errorf("Name = %v, want New", got.Name)
	}
}

func TestUpdate_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Old"),
	})
	_, err := st.svc.Update(ctx, conversation.UpdateParams{
		Actor: b.ID, ConvID: created.Conversation.ID, Name: ptr("New"),
	})
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

// TestUpdate_NonMemberSeesNotFoundEvenForDirect verifies the
// enumeration-leak fix CodeRabbit caught on PR #35: a non-member calling
// Update on a direct conversation must NOT receive Forbidden ("only
// group conversations are mutable") because that reveals the
// conversation exists. They must see NotFound.
func TestUpdate_NonMemberSeesNotFoundEvenForDirect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	_, err := st.svc.Update(ctx, conversation.UpdateParams{
		Actor: stranger.ID, ConvID: created.Conversation.ID, Name: ptr("Hi"),
	})
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND (no enumeration)", asAPIError(t, err).Code)
	}
}

func TestAddMembers_NonMemberSeesNotFoundEvenForDirect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	target := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	_, err := st.svc.AddMembers(ctx, conversation.AddMembersParams{
		Actor: stranger.ID, ConvID: created.Conversation.ID, UserIDs: []uuid.UUID{target.ID},
	})
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND (no enumeration)", asAPIError(t, err).Code)
	}
}

func TestRemoveMember_NonMemberSeesNotFoundEvenForDirect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	err := st.svc.RemoveMember(ctx, stranger.ID, created.Conversation.ID, b.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND (no enumeration)", asAPIError(t, err).Code)
	}
}

func TestUpdate_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Old"),
	})
	_, err := st.svc.Update(ctx, conversation.UpdateParams{
		Actor: a.ID, ConvID: created.Conversation.ID, Name: ptr(""),
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestCreate_GroupNameUsesRuneCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	// 80 emoji — each emoji is several bytes, so byte-length would
	// trip TOO_LONG. Rune count must be exactly 80, accepted.
	emojis := strings.Repeat("🐱", 80)
	if _, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: &emojis,
	}); err != nil {
		t.Errorf("80 emoji should pass rune-count validation, got %v", err)
	}
	// 81 emoji — must reject as TOO_LONG.
	tooLong := strings.Repeat("🐱", 81)
	c := makeUser(ctx, t, st)
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{c.ID}, Name: &tooLong,
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED on 81-rune name", asAPIError(t, err).Code)
	}
}

func TestAddMembers_SkipsExistingMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Group"),
	})

	// Pass {b (already in), c (new)}. Only c should be added.
	got, err := st.svc.AddMembers(ctx, conversation.AddMembersParams{
		Actor: a.ID, ConvID: created.Conversation.ID, UserIDs: []uuid.UUID{b.ID, c.ID},
	})
	if err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if len(got.Added) != 1 || got.Added[0].UserID != c.ID {
		t.Errorf("Added = %+v, want exactly [c]", got.Added)
	}
}

func TestUpdate_DirectIsImmutable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	_, err := st.svc.Update(ctx, conversation.UpdateParams{
		Actor: a.ID, ConvID: created.Conversation.ID, Name: ptr("Cant"),
	})
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

// --- Leave + RemoveMember ----------------------------------------------

func TestLeave_RemovesMembership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	if err := st.svc.Leave(ctx, a.ID, created.Conversation.ID); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if _, err := st.convs.GetMember(ctx, created.Conversation.ID, a.ID); !errors.Is(err, convrepo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	// Other party still sees it.
	if _, err := st.svc.Get(ctx, b.ID, created.Conversation.ID); err != nil {
		t.Errorf("other party can no longer see direct after leave: %v", err)
	}
}

func TestLeave_NonMemberReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	err := st.svc.Leave(ctx, c.ID, created.Conversation.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestSetMute_RoundTripPerMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})

	until := time.Now().Add(15 * time.Minute)
	got, err := st.svc.SetMute(ctx, a.ID, created.Conversation.ID, &until)
	if err != nil {
		t.Fatalf("SetMute: %v", err)
	}
	if got.MutedUntil == nil || !got.MutedUntil.Equal(until.Truncate(time.Microsecond)) {
		t.Errorf("MutedUntil = %v, want %v", got.MutedUntil, until)
	}

	// Other party's row is NOT muted — per-member.
	other, err := st.convs.GetMember(ctx, created.Conversation.ID, b.ID)
	if err != nil {
		t.Fatalf("GetMember(b): %v", err)
	}
	if other.MutedUntil != nil {
		t.Errorf("b.MutedUntil = %v, want nil — mute is per-member", other.MutedUntil)
	}

	// Unmute by passing nil.
	got2, err := st.svc.SetMute(ctx, a.ID, created.Conversation.ID, nil)
	if err != nil {
		t.Fatalf("Unmute: %v", err)
	}
	if got2.MutedUntil != nil {
		t.Errorf("MutedUntil after unmute = %v, want nil", got2.MutedUntil)
	}
}

func TestSetMute_NonMemberReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	until := time.Now().Add(time.Hour)
	_, err := st.svc.SetMute(ctx, stranger.ID, created.Conversation.ID, &until)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestSetPin_RoundTripPerMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})

	got, err := st.svc.SetPin(ctx, a.ID, created.Conversation.ID, true)
	if err != nil {
		t.Fatalf("SetPin: %v", err)
	}
	if got.PinnedAt == nil {
		t.Errorf("PinnedAt = nil, want non-nil after pin")
	}
	// Per-member: b's row stays unpinned.
	other, err := st.convs.GetMember(ctx, created.Conversation.ID, b.ID)
	if err != nil {
		t.Fatalf("GetMember(b): %v", err)
	}
	if other.PinnedAt != nil {
		t.Errorf("b.PinnedAt = %v, want nil — pin is per-member", other.PinnedAt)
	}

	// Unpin.
	got2, err := st.svc.SetPin(ctx, a.ID, created.Conversation.ID, false)
	if err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if got2.PinnedAt != nil {
		t.Errorf("PinnedAt after unpin = %v, want nil", got2.PinnedAt)
	}
}

// Pinned conversations float to the top of the caller's list, ordered
// by pinned_at DESC. Unpinned ones follow, ordered by last_message_at
// DESC. Verifies the §6.2 server-side ordering contract — CodeRabbit
// on PR #101 flagged that without this the pin endpoint persisted
// state but the list didn't reflect it.
func TestList_PinnedFirstOrdering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)

	// Create three groups with a as member. Pin the second one.
	g1 := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
		Name: ptr("Old chat"),
	})
	g2 := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
		Name: ptr("Pinned chat"),
	})
	g3 := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID, MemberIDs: []uuid.UUID{c.ID},
		Name: ptr("New chat"),
	})

	// Touch g1 + g3 so their last_message_at is meaningful order.
	if err := st.convs.TouchLastMessageAt(ctx, g1.Conversation.ID, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("touch g1: %v", err)
	}
	if err := st.convs.TouchLastMessageAt(ctx, g3.Conversation.ID, time.Now()); err != nil {
		t.Fatalf("touch g3: %v", err)
	}

	if _, err := st.svc.SetPin(ctx, a.ID, g2.Conversation.ID, true); err != nil {
		t.Fatalf("pin g2: %v", err)
	}

	res, err := st.svc.List(ctx, conversation.ListParams{UserID: a.ID, Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Conversations) != 3 {
		t.Fatalf("len = %d, want 3", len(res.Conversations))
	}
	// g2 (pinned) first, then g3 (newer last_message_at), then g1.
	if res.Conversations[0].ID != g2.Conversation.ID {
		t.Errorf("pos 0 = %s, want g2 (pinned)", res.Conversations[0].ID)
	}
	if res.Conversations[1].ID != g3.Conversation.ID {
		t.Errorf("pos 1 = %s, want g3 (newest unpinned)", res.Conversations[1].ID)
	}
	if res.Conversations[2].ID != g1.Conversation.ID {
		t.Errorf("pos 2 = %s, want g1 (oldest)", res.Conversations[2].ID)
	}
}

// Pinning an item with `pinned: false` (a *bool, where false is a real
// value not "omitted") clears an existing pin. Verifies the
// SetPinRequest *bool change — CodeRabbit on PR #101.
func TestSetPin_FalseValueClearsPin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})

	if _, err := st.svc.SetPin(ctx, a.ID, created.Conversation.ID, true); err != nil {
		t.Fatalf("pin: %v", err)
	}
	got, err := st.svc.SetPin(ctx, a.ID, created.Conversation.ID, false)
	if err != nil {
		t.Fatalf("unpin via false: %v", err)
	}
	if got.PinnedAt != nil {
		t.Errorf("PinnedAt = %v after pinned:false, want nil", got.PinnedAt)
	}
}

func TestRemoveMember_AdminCanKick(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Group"),
	})
	if err := st.svc.RemoveMember(ctx, a.ID, created.Conversation.ID, b.ID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := st.convs.GetMember(ctx, created.Conversation.ID, b.ID); !errors.Is(err, convrepo.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRemoveMember_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID, c.ID}, Name: ptr("Group"),
	})
	err := st.svc.RemoveMember(ctx, b.ID, created.Conversation.ID, c.ID)
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

func TestRemoveMember_SelfWorks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Group"),
	})
	// b removes themselves (Leave path).
	if err := st.svc.RemoveMember(ctx, b.ID, created.Conversation.ID, b.ID); err != nil {
		t.Fatalf("RemoveMember self: %v", err)
	}
}

// --- AddMembers -------------------------------------------------------

func TestAddMembers_AdminAdds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Group"),
	})
	got, err := st.svc.AddMembers(ctx, conversation.AddMembersParams{
		Actor: a.ID, ConvID: created.Conversation.ID, UserIDs: []uuid.UUID{c.ID},
	})
	if err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if len(got.Added) != 1 || got.Added[0].UserID != c.ID {
		t.Errorf("Added = %+v, want [c]", got.Added)
	}
}

// Anyone in the group can add — Wakeup groups are friend circles,
// not admin-gated workspaces. Non-admin caller successfully adds
// a third user here; previously this surface returned Forbidden.
func TestAddMembers_AnyMemberCanAdd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Group"),
	})
	res, err := st.svc.AddMembers(ctx, conversation.AddMembersParams{
		Actor: b.ID, ConvID: created.Conversation.ID, UserIDs: []uuid.UUID{c.ID},
	})
	if err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if len(res.Added) != 1 || res.Added[0].UserID != c.ID {
		t.Errorf("Added = %+v", res.Added)
	}
}

func TestAddMembers_DirectForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	_, err := st.svc.AddMembers(ctx, conversation.AddMembersParams{
		Actor: a.ID, ConvID: created.Conversation.ID, UserIDs: []uuid.UUID{c.ID},
	})
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
	}
}

func TestAddMembers_OverflowReturnsConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)

	// Build a near-full group: creator + 24 members = 25 total (cap).
	others := make([]uuid.UUID, convrepo.MaxGroupMembers-1)
	for i := range others {
		others[i] = makeUser(ctx, t, st).ID
	}
	created, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: others, Name: ptr("Full"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// One more triggers the cap.
	overflow := makeUser(ctx, t, st).ID
	_, err = st.svc.AddMembers(ctx, conversation.AddMembersParams{
		Actor: a.ID, ConvID: created.Conversation.ID, UserIDs: []uuid.UUID{overflow},
	})
	if asAPIError(t, err).Code != apierror.CodeConflict {
		t.Errorf("Code = %q, want CONFLICT", asAPIError(t, err).Code)
	}
}

// --- MarkRead ----------------------------------------------------------

func TestMarkRead_NonMemberReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	err := st.svc.MarkRead(ctx, c.ID, created.Conversation.ID, uuid.Must(uuid.NewV7()))
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// List pagination: 3 conversations, request limit=2 → page 1 has
// HasMore=true with a cursor, page 2 walks the cursor and returns
// the remaining row with HasMore=false. Covers both the over-fetch
// branch and the terminal-page branch, plus asserts the two pages
// don't overlap.
func TestList_PaginatesPastLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	for i := 0; i < 3; i++ {
		other := makeUser(ctx, t, st)
		mustCreate(ctx, t, st, conversation.CreateParams{
			Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{other.ID},
		})
		// Spread the last_message_at timestamps so List's ordering is
		// deterministic — without distinct timestamps the order is
		// stable but the cursor still needs distinct points to walk.
		time.Sleep(2 * time.Millisecond)
	}
	first, err := st.svc.List(ctx, conversation.ListParams{UserID: a.ID, Limit: 2})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(first.Conversations) != 2 {
		t.Errorf("page 1 len = %d, want 2", len(first.Conversations))
	}
	if !first.HasMore || first.NextCursor == nil {
		t.Fatalf("page 1 expected HasMore=true with cursor, got hasMore=%v cursor=%v", first.HasMore, first.NextCursor)
	}

	cursor, err := pagination.Decode(*first.NextCursor)
	if err != nil {
		t.Fatalf("decode page 1 cursor: %v", err)
	}
	second, err := st.svc.List(ctx, conversation.ListParams{
		UserID: a.ID, Limit: 2, Cursor: cursor,
	})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(second.Conversations) != 1 {
		t.Errorf("page 2 len = %d, want 1", len(second.Conversations))
	}
	if second.HasMore || second.NextCursor != nil {
		t.Errorf("page 2 expected terminal pagination, got hasMore=%v cursor=%v", second.HasMore, second.NextCursor)
	}
	pageOne := map[uuid.UUID]struct{}{}
	for _, c := range first.Conversations {
		pageOne[c.ID] = struct{}{}
	}
	for _, c := range second.Conversations {
		if _, dup := pageOne[c.ID]; dup {
			t.Errorf("conversation %s appeared on both pages", c.ID)
		}
	}
}

// Direct conversations: only self-removal is allowed. An admin
// trying to remove the other party gets Forbidden, not NotFound, even
// though the surface area for the leak is small (they ARE a member).
func TestRemoveMember_DirectOtherIsForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	err := st.svc.RemoveMember(ctx, a.ID, created.Conversation.ID, b.ID)
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q, want FORBIDDEN", asAPIError(t, err).Code)
	}
}

// AddMembers with only-existing members short-circuits (no users
// fetched, no rows added). Covers the candidates-empty fast path that
// SkipsExistingMembers can't reach because it always has one new id.
func TestAddMembers_AllExistingShortCircuits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Group"),
	})
	got, err := st.svc.AddMembers(ctx, conversation.AddMembersParams{
		Actor: a.ID, ConvID: created.Conversation.ID, UserIDs: []uuid.UUID{b.ID},
	})
	if err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if len(got.Added) != 0 {
		t.Errorf("Added = %+v, want empty", got.Added)
	}
}

// Update with name=nil must succeed without modifying the row —
// covers the allowNil-true return path in validateGroupName plus the
// pass-through behavior of UpdateConversation when only AvatarURL is
// set. The existing rename test always supplies a name.
func TestUpdate_NameNilLeavesNameUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, Name: ptr("Original"),
	})
	avatar := "https://example.test/a.png"
	got, err := st.svc.Update(ctx, conversation.UpdateParams{
		Actor: a.ID, ConvID: created.Conversation.ID,
		Name: nil, AvatarURL: &avatar,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name == nil || *got.Name != "Original" {
		t.Errorf("Name = %v, want unchanged 'Original'", got.Name)
	}
	if got.AvatarURL == nil || *got.AvatarURL != avatar {
		t.Errorf("AvatarURL = %v, want %q", got.AvatarURL, avatar)
	}
}

// Self-removal then re-removal: Leave's idempotent RemoveMember
// branch fires when the row vanishes between the membership check
// and the actual delete. We can't deterministically race it, but a
// second self-Leave on a left conversation surfaces the
// NotFound→nil-from-Leave's caller side: even though the second
// Leave returns NotFound at the GetMember step, this still exercises
// the idempotent code path through testutil's reuse pattern. The
// test pins observed behavior so a regression that returned an error
// instead of nil-on-already-removed surfaces here.
func TestLeave_AfterAlreadyLeft(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	if err := st.svc.Leave(ctx, a.ID, created.Conversation.ID); err != nil {
		t.Fatalf("first Leave: %v", err)
	}
	// After leaving, the user is no longer a member; Leave returns
	// NotFound — the same observable shape a stranger sees.
	err := st.svc.Leave(ctx, a.ID, created.Conversation.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// Removing a non-member from a group surfaces NotFound("member") —
// distinguishing "you can't see this conversation" (NotFound on first
// GetMember) from "this user isn't in the conversation" (NotFound on
// the second).
func TestRemoveMember_TargetNotInGroupReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	creator := makeUser(ctx, t, st)
	other := makeUser(ctx, t, st)
	bystander := makeUser(ctx, t, st)
	name := "Crew"
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: creator.ID, MemberIDs: []uuid.UUID{other.ID},
		Name: &name,
	})
	err := st.svc.RemoveMember(ctx, creator.ID, created.Conversation.ID, bystander.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// MarkRead happy path: a member's read pointer updates without error.
// Doesn't validate the message belongs to the conversation (per spec
// — that's the message service's responsibility), but the schema's
// FK to messages(id) means the message row must actually exist.
func TestMarkRead_MemberSucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	msgID := uuid.Must(uuid.NewV7())
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO messages (id, conversation_id, sender_id, body)
		VALUES ($1, $2, $3, 'hello')
	`, msgID, created.Conversation.ID, a.ID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	if err := st.svc.MarkRead(ctx, b.ID, created.Conversation.ID, msgID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
}

// ListMembersForConversations batch-loads members, keyed by
// conversation_id. Empty input returns an empty map (no DB round
// trip), and the result preserves all conversations the caller
// actually requested.
func TestListMembersForConversations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c := makeUser(ctx, t, st)
	convA := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	}).Conversation.ID
	name := "Crew"
	convB := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID, c.ID},
		Name: &name,
	}).Conversation.ID

	got, err := st.svc.ListMembersForConversations(ctx, []uuid.UUID{convA, convB})
	if err != nil {
		t.Fatalf("ListMembersForConversations: %v", err)
	}
	if len(got[convA]) != 2 {
		t.Errorf("convA members = %d, want 2", len(got[convA]))
	}
	if len(got[convB]) != 3 {
		t.Errorf("convB members = %d, want 3", len(got[convB]))
	}

	// Empty input must NOT hit the DB; should return an empty map.
	empty, err := st.svc.ListMembersForConversations(ctx, nil)
	if err != nil {
		t.Fatalf("empty ListMembersForConversations: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty input map size = %d, want 0", len(empty))
	}
}

// Closed-pool sweep — every public method's apierror.Internal wrap
// fires once. Pool.Close() makes every repo query fail-fast, which is
// the cheapest way to flush the symmetric error-wrapping branches.
func TestService_DBClosedReturnsInternal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	created := mustCreate(ctx, t, st, conversation.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	convID := created.Conversation.ID
	st.pool.Close()

	checks := []struct {
		name string
		fn   func() error
	}{
		{"Create", func() error {
			_, err := st.svc.Create(ctx, conversation.CreateParams{
				Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
			})
			return err
		}},
		{"Get", func() error {
			_, err := st.svc.Get(ctx, a.ID, convID)
			return err
		}},
		{"List", func() error {
			_, err := st.svc.List(ctx, conversation.ListParams{UserID: a.ID, Limit: 10})
			return err
		}},
		{"Update", func() error {
			_, err := st.svc.Update(ctx, conversation.UpdateParams{
				Actor: a.ID, ConvID: convID, Name: ptr("x"),
			})
			return err
		}},
		{"Leave", func() error {
			return st.svc.Leave(ctx, a.ID, convID)
		}},
		{"AddMembers", func() error {
			_, err := st.svc.AddMembers(ctx, conversation.AddMembersParams{
				Actor: a.ID, ConvID: convID, UserIDs: []uuid.UUID{uuid.New()},
			})
			return err
		}},
		{"RemoveMember", func() error {
			return st.svc.RemoveMember(ctx, a.ID, convID, b.ID)
		}},
		{"MarkRead", func() error {
			return st.svc.MarkRead(ctx, a.ID, convID, uuid.Must(uuid.NewV7()))
		}},
		{"ListMembersForConversations", func() error {
			_, err := st.svc.ListMembersForConversations(ctx, []uuid.UUID{convID})
			return err
		}},
	}
	for _, c := range checks {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := c.fn()
			if err == nil {
				t.Fatalf("%s: expected error against closed pool", c.name)
			}
			if asAPIError(t, err).Code != apierror.CodeInternal {
				t.Errorf("%s: Code = %q, want INTERNAL_ERROR", c.name, asAPIError(t, err).Code)
			}
		})
	}
}

// --- Config validation -------------------------------------------------

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  conversation.Config
	}{
		{"nil all", conversation.Config{}},
		{"nil convs", conversation.Config{Pool: &pgxpool.Pool{}, Users: &userrepo.Queries{}}},
		{"nil users", conversation.Config{Pool: &pgxpool.Pool{}, Convs: &convrepo.Queries{}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := conversation.New(tc.cfg); err == nil {
				t.Error("expected error")
			}
		})
	}
}
