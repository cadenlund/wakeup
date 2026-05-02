package attachment_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/attachment"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	msgrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL.
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

// makeDirect inserts a direct conversation with both members.
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

// makeAttachment inserts via the repo and returns the row.
func makeAttachment(ctx context.Context, t *testing.T, repo *attachment.Queries, uploader uuid.UUID) domain.Attachment {
	t.Helper()
	got, err := repo.Create(ctx, attachment.CreateParams{
		ID: uuid.Must(uuid.NewV7()), UploaderID: uploader,
		StorageKey: "attachments/" + uuid.Must(uuid.NewV7()).String() + "/file.bin",
		Filename:   "file.bin", ContentType: "application/octet-stream",
		SizeBytes: 12,
	})
	if err != nil {
		t.Fatalf("makeAttachment: %v", err)
	}
	return got
}

// linkAttachment inserts a (message_id, attachment_id) row.
func linkAttachment(ctx context.Context, t *testing.T, pool *pgxpool.Pool, messageID, attachmentID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO message_attachments (message_id, attachment_id) VALUES ($1, $2)`,
		messageID, attachmentID)
	if err != nil {
		t.Fatalf("linkAttachment: %v", err)
	}
}

// --- Create + GetByID -------------------------------------------------

func TestCreate_GetByID_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	uploader := makeUser(ctx, t, pool)

	created, err := repo.Create(ctx, attachment.CreateParams{
		ID: uuid.Must(uuid.NewV7()), UploaderID: uploader,
		StorageKey:  "attachments/abc/x.png",
		Filename:    "x.png",
		ContentType: "image/png",
		SizeBytes:   42,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Filename != "x.png" || created.ContentType != "image/png" || created.SizeBytes != 42 {
		t.Errorf("created = %+v", created)
	}
	if created.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be set")
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GetByID id = %v, want %v", got.ID, created.ID)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	_, err := repo.GetByID(ctx, uuid.New())
	if !errors.Is(err, attachment.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreate_RejectsZeroSize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	uploader := makeUser(ctx, t, pool)
	_, err := repo.Create(ctx, attachment.CreateParams{
		ID: uuid.Must(uuid.NewV7()), UploaderID: uploader,
		StorageKey: "k", Filename: "f", ContentType: "x", SizeBytes: 0,
	})
	if err == nil {
		t.Fatal("expected CHECK violation for size_bytes=0")
	}
}

// --- ListOrphansOlderThan + DeleteByIDs -------------------------------

func TestListOrphansOlderThan_FiltersByCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	uploader := makeUser(ctx, t, pool)

	old := makeAttachment(ctx, t, repo, uploader)
	_ = makeAttachment(ctx, t, repo, uploader) // newer, also orphan

	// Force `old` to look truly old by rolling its created_at back.
	if _, err := pool.Exec(ctx,
		`UPDATE attachments SET created_at = $2 WHERE id = $1`,
		old.ID, time.Now().Add(-25*time.Hour),
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got, err := repo.ListOrphansOlderThan(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListOrphansOlderThan: %v", err)
	}
	if len(got) != 1 || got[0].ID != old.ID {
		t.Errorf("orphans = %+v, want only %v", got, old.ID)
	}
}

func TestListOrphansOlderThan_ExcludesLinked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	att := makeAttachment(ctx, t, repo, a)

	// Send a message and link the attachment.
	mr := msgrepo.New(pool)
	msg, err := mr.Create(ctx, msgrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: cid, SenderID: a, Body: "x",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	linkAttachment(ctx, t, pool, msg.ID, att.ID)

	// Backdate so the cutoff would otherwise include it.
	if _, err := pool.Exec(ctx,
		`UPDATE attachments SET created_at = $2 WHERE id = $1`,
		att.ID, time.Now().Add(-72*time.Hour),
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got, err := repo.ListOrphansOlderThan(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListOrphansOlderThan: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("orphans = %+v, want 0 (linked attachment is not an orphan)", got)
	}
}

func TestDeleteByIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	uploader := makeUser(ctx, t, pool)

	a1 := makeAttachment(ctx, t, repo, uploader)
	a2 := makeAttachment(ctx, t, repo, uploader)

	if err := repo.DeleteByIDs(ctx, []uuid.UUID{a1.ID, a2.ID}); err != nil {
		t.Fatalf("DeleteByIDs: %v", err)
	}
	if _, err := repo.GetByID(ctx, a1.ID); !errors.Is(err, attachment.ErrNotFound) {
		t.Errorf("a1 still exists: %v", err)
	}
	if _, err := repo.GetByID(ctx, a2.ID); !errors.Is(err, attachment.ErrNotFound) {
		t.Errorf("a2 still exists: %v", err)
	}
}

func TestDeleteByIDs_EmptySliceNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	if err := repo.DeleteByIDs(ctx, nil); err != nil {
		t.Fatalf("DeleteByIDs(nil): %v", err)
	}
}

// --- CallerCanRead — every branch -------------------------------------

func TestCallerCanRead_OrphanByUploader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	uploader := makeUser(ctx, t, pool)

	att := makeAttachment(ctx, t, repo, uploader)

	ok, err := repo.CallerCanRead(ctx, att.ID, uploader)
	if err != nil {
		t.Fatalf("CallerCanRead: %v", err)
	}
	if !ok {
		t.Errorf("uploader should be able to read their own orphan")
	}
}

func TestCallerCanRead_OrphanNonUploaderDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	uploader := makeUser(ctx, t, pool)
	stranger := makeUser(ctx, t, pool)

	att := makeAttachment(ctx, t, repo, uploader)

	ok, err := repo.CallerCanRead(ctx, att.ID, stranger)
	if err != nil {
		t.Fatalf("CallerCanRead: %v", err)
	}
	if ok {
		t.Errorf("stranger must NOT read uploader's orphan attachment")
	}
}

func TestCallerCanRead_LinkedMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	att := makeAttachment(ctx, t, repo, a)

	mr := msgrepo.New(pool)
	msg, err := mr.Create(ctx, msgrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: cid, SenderID: a, Body: "x",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	linkAttachment(ctx, t, pool, msg.ID, att.ID)

	// Both members can read; the uploader (a) AND the other member (b).
	for _, who := range []uuid.UUID{a, b} {
		ok, err := repo.CallerCanRead(ctx, att.ID, who)
		if err != nil {
			t.Fatalf("CallerCanRead(%v): %v", who, err)
		}
		if !ok {
			t.Errorf("member %v should be able to read linked attachment", who)
		}
	}
}

func TestCallerCanRead_LinkedNonMemberDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	stranger := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	att := makeAttachment(ctx, t, repo, a)

	mr := msgrepo.New(pool)
	msg, err := mr.Create(ctx, msgrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: cid, SenderID: a, Body: "x",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	linkAttachment(ctx, t, pool, msg.ID, att.ID)

	ok, err := repo.CallerCanRead(ctx, att.ID, stranger)
	if err != nil {
		t.Fatalf("CallerCanRead: %v", err)
	}
	if ok {
		t.Errorf("stranger must NOT read attachment in conversation they're not in")
	}
}

func TestCallerCanRead_OnceLinkedUploaderStillCanReadViaMembership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	a := makeUser(ctx, t, pool)
	b := makeUser(ctx, t, pool)
	cid := makeDirect(ctx, t, pool, a, b)
	att := makeAttachment(ctx, t, repo, a)

	mr := msgrepo.New(pool)
	msg, err := mr.Create(ctx, msgrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ConversationID: cid, SenderID: a, Body: "x",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}
	linkAttachment(ctx, t, pool, msg.ID, att.ID)

	// Once linked, the orphan branch no longer applies — but the uploader
	// is also a conversation member, so reads still go through the linked
	// branch. Regression guard against a future schema where uploader
	// leaves the conversation.
	ok, err := repo.CallerCanRead(ctx, att.ID, a)
	if err != nil {
		t.Fatalf("CallerCanRead: %v", err)
	}
	if !ok {
		t.Errorf("uploader-as-member should still read linked attachment")
	}
}

func TestCallerCanRead_NonexistentAttachmentReturnsFalseNoError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := attachment.New(pool)
	uploader := makeUser(ctx, t, pool)

	ok, err := repo.CallerCanRead(ctx, uuid.New(), uploader)
	if err != nil {
		t.Fatalf("CallerCanRead: %v", err)
	}
	if ok {
		t.Errorf("nonexistent attachment must be unreadable")
	}
}
