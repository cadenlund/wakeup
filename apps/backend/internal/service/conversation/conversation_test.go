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
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

type stack struct {
	svc   *conversation.Service
	convs *convrepo.Queries
	users *userrepo.Queries
	pool  *pgxpool.Pool
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	convs := convrepo.New(pool)
	users := userrepo.New(pool)
	svc, err := conversation.New(conversation.Config{Pool: pool, Convs: convs, Users: users})
	if err != nil {
		t.Fatalf("conversation.New: %v", err)
	}
	return &stack{svc: svc, convs: convs, users: users, pool: pool}
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

func TestCreate_GroupRequiresName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	_, err := st.svc.Create(ctx, conversation.CreateParams{
		Type: domain.ConversationGroup, Creator: a.ID,
		MemberIDs: []uuid.UUID{b.ID}, // no name
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

func TestAddMembers_NonAdminForbidden(t *testing.T) {
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
	_, err := st.svc.AddMembers(ctx, conversation.AddMembersParams{
		Actor: b.ID, ConvID: created.Conversation.ID, UserIDs: []uuid.UUID{c.ID},
	})
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q", asAPIError(t, err).Code)
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

// --- Config validation -------------------------------------------------

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := conversation.New(conversation.Config{}); err == nil {
		t.Error("nil deps should error")
	}
}
