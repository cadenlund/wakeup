package conversation_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

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

// makeDirect inserts a direct conversation + the two member rows.
// Returns the conversation ID.
func makeDirect(ctx context.Context, t *testing.T, repo *conversation.Queries, a, b uuid.UUID) uuid.UUID {
	t.Helper()
	c, err := repo.CreateConversation(ctx, conversation.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationDirect, CreatedBy: a,
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if _, err := repo.AddMember(ctx, c.ID, a, domain.MemberRoleMember); err != nil {
		t.Fatalf("AddMember a: %v", err)
	}
	if _, err := repo.AddMember(ctx, c.ID, b, domain.MemberRoleMember); err != nil {
		t.Fatalf("AddMember b: %v", err)
	}
	return c.ID
}

// --- conversation table ------------------------------------------------

func TestCreateConversation_Direct(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)

	got, err := repo.CreateConversation(ctx, conversation.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationDirect, CreatedBy: a,
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if got.Type != domain.ConversationDirect {
		t.Errorf("Type = %q, want direct", got.Type)
	}
	if got.Name != nil {
		t.Errorf("Name should be nil for direct, got %v", got.Name)
	}
}

func TestCreateConversation_Group(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	name := "Wakeup Crew"

	got, err := repo.CreateConversation(ctx, conversation.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationGroup, Name: &name, CreatedBy: a,
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if got.Type != domain.ConversationGroup {
		t.Errorf("Type = %q, want group", got.Type)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %s", got.Name, name)
	}
}

func TestCreateConversation_RejectsBogusType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	_, err := repo.CreateConversation(ctx, conversation.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationType("bogus"), CreatedBy: a,
	})
	if err == nil {
		t.Fatal("CHECK violation expected for bogus type")
	}
}

func TestGetConversation_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	_, err := repo.GetConversation(ctx, uuid.New())
	if !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestUpdateConversation_PatchesName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	orig := "Old"
	c, _ := repo.CreateConversation(ctx, conversation.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationGroup, Name: &orig, CreatedBy: a,
	})
	newName := "New"
	got, err := repo.UpdateConversation(ctx, conversation.UpdateParams{
		ID: c.ID, Name: &newName,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name == nil || *got.Name != newName {
		t.Errorf("Name = %v, want %s", got.Name, newName)
	}
}

func TestTouchLastMessageAt_BumpsForward(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	c, _ := repo.CreateConversation(ctx, conversation.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationDirect, CreatedBy: a,
	})

	future := time.Now().Add(1 * time.Hour).UTC()
	if err := repo.TouchLastMessageAt(ctx, c.ID, future); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, _ := repo.GetConversation(ctx, c.ID)
	if got.LastMessageAt.Before(future.Add(-time.Second)) {
		t.Errorf("LastMessageAt = %v, want >= %v", got.LastMessageAt, future)
	}
}

func TestTouchLastMessageAt_OlderIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	c, _ := repo.CreateConversation(ctx, conversation.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationDirect, CreatedBy: a,
	})
	before := c.LastMessageAt
	older := before.Add(-1 * time.Hour)
	if err := repo.TouchLastMessageAt(ctx, c.ID, older); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, _ := repo.GetConversation(ctx, c.ID)
	if !got.LastMessageAt.Equal(before) {
		t.Errorf("LastMessageAt regressed: was %v, now %v", before, got.LastMessageAt)
	}
}

// --- members + listing -------------------------------------------------

func TestAddMember_PrimaryKeyEnforced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, repo, a, b)
	// re-adding a (already a member) should fail on PK.
	if _, err := repo.AddMember(ctx, cid, a, domain.MemberRoleMember); err == nil {
		t.Fatal("expected PK violation on duplicate AddMember")
	}
}

func TestRemoveMember_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, repo, a, b)
	other := makeUser(ctx, t, pool)
	if err := repo.RemoveMember(ctx, cid, other); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestGetMember_BothLookups(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, repo, a, b)

	got, err := repo.GetMember(ctx, cid, a)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if got.UserID != a {
		t.Errorf("UserID = %s, want %s", got.UserID, a)
	}
	if _, err := repo.GetMember(ctx, cid, makeUser(ctx, t, pool)); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("non-member should be ErrNotFound, got %v", err)
	}
}

func TestListMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, repo, a, b)

	got, err := repo.ListMembers(ctx, cid)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestCountMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, repo, a, b)
	n, err := repo.CountMembers(ctx, cid)
	if err != nil {
		t.Fatalf("CountMembers: %v", err)
	}
	if n != 2 {
		t.Errorf("n = %d, want 2", n)
	}
}

// --- pagination + direct lookup ----------------------------------------

func TestListConversationsByUser_PaginatedNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	me := makeUser(ctx, t, pool)
	for i := 0; i < 5; i++ {
		other := makeUser(ctx, t, pool)
		_ = makeDirect(ctx, t, repo, me, other)
	}
	got, err := repo.ListConversationsByUser(ctx, me, nil, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("over-fetch len = %d, want 4 (limit+1)", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].LastMessageAt.After(got[i-1].LastMessageAt) {
			t.Errorf("rows not in DESC last_message_at order: %v vs %v", got[i-1].LastMessageAt, got[i].LastMessageAt)
		}
	}
}

func TestGetDirectByPair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, repo, a, b)

	got, err := repo.GetDirectByPair(ctx, a, b)
	if err != nil {
		t.Fatalf("GetDirectByPair: %v", err)
	}
	if got.ID != cid {
		t.Errorf("id = %s, want %s", got.ID, cid)
	}
	// Reversed order finds the same row.
	got2, err := repo.GetDirectByPair(ctx, b, a)
	if err != nil {
		t.Fatalf("GetDirectByPair reversed: %v", err)
	}
	if got2.ID != cid {
		t.Errorf("reversed id = %s, want %s", got2.ID, cid)
	}
}

func TestGetDirectByPair_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	if _, err := repo.GetDirectByPair(ctx, a, b); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// --- mark read ---------------------------------------------------------
//
// UpdateLastReadMessage's behavior with a real message id is exercised by
// the message repository tests (Phase 6) — `last_read_message_id` has an
// FK to messages which we don't have at this layer. We do verify here
// that the column starts NULL, and the SQL+API surface is exercised in
// the integration tests once the message repo lands.

func TestMember_LastReadMessageStartsNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, repo, a, b)
	got, err := repo.GetMember(ctx, cid, a)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if got.LastReadMessageID != nil {
		t.Errorf("LastReadMessageID should start nil, got %v", got.LastReadMessageID)
	}
}

// --- cascade -----------------------------------------------------------
//
// `conversations.created_by` is intentionally NOT ON DELETE CASCADE per
// §4.6 — soft-deleted users keep their content. Only conversation_members
// has CASCADE; we test that path here.

func TestCascadeDeleteUserRemovesMembership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	creator := makeUser(ctx, t, pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)

	groupName := "Crew"
	c, err := repo.CreateConversation(ctx, conversation.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationGroup,
		Name: &groupName, CreatedBy: creator,
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if _, err := repo.AddMember(ctx, c.ID, a, domain.MemberRoleMember); err != nil {
		t.Fatalf("AddMember a: %v", err)
	}
	if _, err := repo.AddMember(ctx, c.ID, b, domain.MemberRoleMember); err != nil {
		t.Fatalf("AddMember b: %v", err)
	}

	// Hard-delete one member — their conversation_members row should
	// vanish via CASCADE. (creator is NOT cascaded — we'd need a soft-
	// delete + DTO collapse for that path, which is exercised at the
	// handler layer.)
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", a); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := repo.GetMember(ctx, c.ID, a); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("member row not cascaded: %v", err)
	}
}

func TestCascadeDeleteConversationRemovesMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := conversation.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, repo, a, b)
	if err := repo.DeleteConversation(ctx, cid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := repo.ListMembers(ctx, cid)
	if len(got) != 0 {
		t.Errorf("members not cascaded: %d remain", len(got))
	}
}

// --- domain helper -----------------------------------------------------

func TestConversation_TypeHelpers(t *testing.T) {
	t.Parallel()
	g := domain.Conversation{Type: domain.ConversationGroup}
	d := domain.Conversation{Type: domain.ConversationDirect}
	if !g.IsGroup() || g.IsDirect() {
		t.Errorf("group helper wrong: %+v", g)
	}
	if !d.IsDirect() || d.IsGroup() {
		t.Errorf("direct helper wrong: %+v", d)
	}
}

func TestMember_AdminHelper(t *testing.T) {
	t.Parallel()
	if !(domain.ConversationMember{Role: domain.MemberRoleAdmin}).IsAdmin() {
		t.Error("admin role helper wrong")
	}
	if (domain.ConversationMember{Role: domain.MemberRoleMember}).IsAdmin() {
		t.Error("member role helper wrong")
	}
}
