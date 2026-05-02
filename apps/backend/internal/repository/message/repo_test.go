package message_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL. We don't use the user repo's
// fixtures because importing it from the message_test package would
// pull in the trigram-search wiring and slow this test down.
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

// makeDirect inserts a direct conversation + 2 member rows so a
// message has a real conversation_id to point at.
func makeDirect(ctx context.Context, t *testing.T, pool *pgxpool.Pool, a, b uuid.UUID) uuid.UUID {
	t.Helper()
	cr := convrepo.New(pool)
	c, err := cr.CreateConversation(ctx, convrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Type: domain.ConversationDirect, CreatedBy: a,
	})
	if err != nil {
		t.Fatalf("makeDirect: create: %v", err)
	}
	if _, err := cr.AddMember(ctx, c.ID, a, domain.MemberRoleMember); err != nil {
		t.Fatalf("makeDirect: add a: %v", err)
	}
	if _, err := cr.AddMember(ctx, c.ID, b, domain.MemberRoleMember); err != nil {
		t.Fatalf("makeDirect: add b: %v", err)
	}
	return c.ID
}

func send(ctx context.Context, t *testing.T, repo *message.Queries, conv, sender uuid.UUID, body string) domain.Message {
	t.Helper()
	got, err := repo.Create(ctx, message.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: conv, SenderID: sender, Body: body,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	return got
}

// --- Create + body CHECK ----------------------------------------------

func TestCreate_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)

	got, err := repo.Create(ctx, message.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: cid, SenderID: a, Body: "hello",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Body != "hello" {
		t.Errorf("Body = %q", got.Body)
	}
	if got.EditedAt != nil || got.DeletedAt != nil {
		t.Errorf("expected nil EditedAt/DeletedAt on fresh row: %+v", got)
	}
}

func TestCreate_EmptyBodyFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	_, err := repo.Create(ctx, message.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: cid, SenderID: a, Body: "",
	})
	if err == nil {
		t.Fatal("expected CHECK violation for empty body")
	}
}

func TestCreate_OversizeBodyFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	_, err := repo.Create(ctx, message.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: cid, SenderID: a,
		Body: strings.Repeat("x", 10001),
	})
	if err == nil {
		t.Fatal("expected CHECK violation for >10000 chars")
	}
}

// --- Get + GetByIDIncludingDeleted ------------------------------------

func TestGet_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	if _, err := repo.GetByID(ctx, uuid.New()); !errors.Is(err, message.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestGet_HidesSoftDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	m := send(ctx, t, repo, cid, a, "hi")

	if err := repo.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := repo.GetByID(ctx, m.ID); !errors.Is(err, message.ErrNotFound) {
		t.Errorf("after SoftDelete, GetByID = %v, want ErrNotFound", err)
	}
	got, err := repo.GetByIDIncludingDeleted(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetByIDIncludingDeleted: %v", err)
	}
	if got.DeletedAt == nil {
		t.Errorf("DeletedAt should be set after SoftDelete")
	}
}

// --- UpdateBody --------------------------------------------------------

func TestUpdateBody_StampsEditedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	m := send(ctx, t, repo, cid, a, "first")

	got, err := repo.UpdateBody(ctx, m.ID, "second")
	if err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	if got.Body != "second" {
		t.Errorf("Body = %q, want second", got.Body)
	}
	if got.EditedAt == nil {
		t.Errorf("EditedAt should be stamped after UpdateBody")
	}
}

func TestUpdateBody_RefusesDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	m := send(ctx, t, repo, cid, a, "hi")
	if err := repo.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := repo.UpdateBody(ctx, m.ID, "edit"); !errors.Is(err, message.ErrNotFound) {
		t.Errorf("UpdateBody on deleted = %v, want ErrNotFound", err)
	}
}

// --- SoftDelete idempotency -------------------------------------------

func TestSoftDelete_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	m := send(ctx, t, repo, cid, a, "hi")

	if err := repo.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("first SoftDelete: %v", err)
	}
	first, err := repo.GetByIDIncludingDeleted(ctx, m.ID)
	if err != nil {
		t.Fatalf("first GetByID: %v", err)
	}
	if err := repo.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("second SoftDelete: %v", err)
	}
	second, err := repo.GetByIDIncludingDeleted(ctx, m.ID)
	if err != nil {
		t.Fatalf("second GetByID: %v", err)
	}
	// Idempotent: deleted_at shouldn't change after the first stamp.
	if !first.DeletedAt.Equal(*second.DeletedAt) {
		t.Errorf("re-delete clobbered deleted_at: first=%v second=%v", first.DeletedAt, second.DeletedAt)
	}
}

// --- ListByConversation: pagination + soft-delete inclusion + search --

func TestListByConversation_PaginatesNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)

	for i := 0; i < 5; i++ {
		send(ctx, t, repo, cid, a, "msg")
	}
	got, err := repo.ListByConversation(ctx, message.ListByConversationParams{
		ConversationID: cid, Limit: 3,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("over-fetch len = %d, want 4 (limit+1)", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("rows out of DESC order at i=%d", i)
		}
	}
}

func TestListByConversation_IncludesSoftDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)

	m1 := send(ctx, t, repo, cid, a, "first")
	m2 := send(ctx, t, repo, cid, a, "second")
	if err := repo.SoftDelete(ctx, m1.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	got, err := repo.ListByConversation(ctx, message.ListByConversationParams{
		ConversationID: cid, Limit: 10,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Both rows should appear; the deleted one carries DeletedAt.
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (deleted included)", len(got))
	}
	var sawDeleted bool
	for _, m := range got {
		if m.ID == m1.ID && m.DeletedAt != nil {
			sawDeleted = true
		}
		if m.ID == m2.ID && m.DeletedAt != nil {
			t.Errorf("non-deleted row leaked DeletedAt")
		}
	}
	if !sawDeleted {
		t.Errorf("deleted row missing from list result")
	}
}

func TestListByConversation_FullTextMatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	send(ctx, t, repo, cid, a, "the quick brown fox jumps")
	send(ctx, t, repo, cid, a, "lazy dog rests in the sun")
	send(ctx, t, repo, cid, a, "completely unrelated phrase")

	got, err := repo.ListByConversation(ctx, message.ListByConversationParams{
		ConversationID: cid, Limit: 10, Query: "fox",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1 match for 'fox'", len(got))
	}
	if !strings.Contains(got[0].Body, "fox") {
		t.Errorf("matched row body = %q, want contains 'fox'", got[0].Body)
	}
}

func TestListByConversation_FullTextStems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	send(ctx, t, repo, cid, a, "running through the field")
	send(ctx, t, repo, cid, a, "no relevant text here")

	// English stemmer maps "ran"/"runs" → "run", same as "running".
	got, err := repo.ListByConversation(ctx, message.ListByConversationParams{
		ConversationID: cid, Limit: 10, Query: "runs",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1 stem-match for 'runs'", len(got))
	}
}

// makeAttachment inserts an attachments row directly so message_attachments
// FK-checks can resolve. The attachments service lands in milestone 7,
// so for now we drop in raw SQL — this matches the migration shape.
func makeAttachment(ctx context.Context, t *testing.T, pool *pgxpool.Pool, uploader uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := pool.Exec(ctx, `
		INSERT INTO attachments (id, uploader_id, storage_key, filename, content_type, size_bytes)
		VALUES ($1, $2, 'attachments/test', 'test.png', 'image/png', 100)
	`, id, uploader)
	if err != nil {
		t.Fatalf("makeAttachment: %v", err)
	}
	return id
}

// --- attachments link --------------------------------------------------

func TestAddAttachment_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	m := send(ctx, t, repo, cid, a, "with attachment")
	att := makeAttachment(ctx, t, pool, a)

	if err := repo.AddAttachment(ctx, m.ID, att); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	// Re-adding the same pair must not error (PK collision swallowed).
	if err := repo.AddAttachment(ctx, m.ID, att); err != nil {
		t.Fatalf("re-AddAttachment: %v", err)
	}
	got, err := repo.ListAttachmentsForMessage(ctx, m.ID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(got) != 1 || got[0] != att {
		t.Errorf("ListAttachments = %+v, want [%s]", got, att)
	}
}

// --- read receipts ----------------------------------------------------

func TestMarkRead_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	m := send(ctx, t, repo, cid, a, "ping")

	if err := repo.MarkRead(ctx, m.ID, b); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	first, err := repo.ListReadsForMessage(ctx, m.ID)
	if err != nil {
		t.Fatalf("ListReads: %v", err)
	}
	if err := repo.MarkRead(ctx, m.ID, b); err != nil {
		t.Fatalf("re-MarkRead: %v", err)
	}
	second, err := repo.ListReadsForMessage(ctx, m.ID)
	if err != nil {
		t.Fatalf("re-ListReads: %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected exactly 1 read per call, got first=%d second=%d", len(first), len(second))
	}
	if !first[0].ReadAt.Equal(second[0].ReadAt) {
		t.Errorf("ReadAt clobbered on re-mark: first=%v second=%v", first[0].ReadAt, second[0].ReadAt)
	}
}

func TestListReadsForMessage_NewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	c := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	m := send(ctx, t, repo, cid, a, "ping")

	if err := repo.MarkRead(ctx, m.ID, b); err != nil {
		t.Fatalf("MarkRead b: %v", err)
	}
	if err := repo.MarkRead(ctx, m.ID, c); err != nil {
		t.Fatalf("MarkRead c: %v", err)
	}
	got, err := repo.ListReadsForMessage(ctx, m.ID)
	if err != nil {
		t.Fatalf("ListReads: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].ReadAt.After(got[i-1].ReadAt) {
			t.Errorf("rows out of DESC order at i=%d", i)
		}
	}
}

// --- cascade ----------------------------------------------------------

func TestCascadeDeleteWithConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := message.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	m := send(ctx, t, repo, cid, a, "to be cascaded")

	cr := convrepo.New(pool)
	if err := cr.DeleteConversation(ctx, cid); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if _, err := repo.GetByIDIncludingDeleted(ctx, m.ID); !errors.Is(err, message.ErrNotFound) {
		t.Errorf("message not cascaded with conversation: %v", err)
	}
}

// --- domain helpers ---------------------------------------------------

func TestMessage_IsDeletedHelper(t *testing.T) {
	t.Parallel()
	if (domain.Message{}).IsDeleted() {
		t.Error("zero message should not report deleted")
	}
	now := time.Now()
	if !(domain.Message{DeletedAt: &now}).IsDeleted() {
		t.Error("DeletedAt non-nil should report deleted")
	}
}

func TestMessage_IsEditedHelper(t *testing.T) {
	t.Parallel()
	if (domain.Message{}).IsEdited() {
		t.Error("zero message should not report edited")
	}
	now := time.Now()
	if !(domain.Message{EditedAt: &now}).IsEdited() {
		t.Error("EditedAt non-nil should report edited")
	}
}
