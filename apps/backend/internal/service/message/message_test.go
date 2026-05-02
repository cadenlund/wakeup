package message_test

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
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	msgrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/message"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

type stack struct {
	svc     *message.Service
	convsvc *convsvc.Service
	convs   *convrepo.Queries
	msgs    *msgrepo.Queries
	users   *userrepo.Queries
	pool    *pgxpool.Pool
	broker  pubsub.Broker
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewTestDB(t)
	convs := convrepo.New(pool)
	msgs := msgrepo.New(pool)
	users := userrepo.New(pool)

	cs, err := convsvc.New(convsvc.Config{Pool: pool, Convs: convs, Users: users})
	if err != nil {
		t.Fatalf("convsvc.New: %v", err)
	}

	broker := pubsub.NewInProc(pubsub.NewRegistry())
	t.Cleanup(func() { _ = broker.Close() })

	svc, err := message.New(message.Config{
		Pool: pool, Msgs: msgs, Convs: convs, Broker: broker,
	})
	if err != nil {
		t.Fatalf("message.New: %v", err)
	}
	return &stack{svc: svc, convsvc: cs, convs: convs, msgs: msgs, users: users, pool: pool, broker: broker}
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

// makeDirect uses the conversation service so the resulting conversation
// is structurally consistent (creator + member rows, etc.).
func makeDirect(ctx context.Context, t *testing.T, st *stack, a, b domain.User) uuid.UUID {
	t.Helper()
	res, err := st.convsvc.Create(ctx, convsvc.CreateParams{
		Type: domain.ConversationDirect, Creator: a.ID, MemberIDs: []uuid.UUID{b.ID},
	})
	if err != nil {
		t.Fatalf("makeDirect: %v", err)
	}
	return res.Conversation.ID
}

// makeGroup creates a group conversation with `creator` as admin and the
// supplied members. Returns the group id.
func makeGroup(ctx context.Context, t *testing.T, st *stack, creator domain.User, members []uuid.UUID) uuid.UUID {
	t.Helper()
	name := "Crew"
	res, err := st.convsvc.Create(ctx, convsvc.CreateParams{
		Type: domain.ConversationGroup, Creator: creator.ID, MemberIDs: members, Name: &name,
	})
	if err != nil {
		t.Fatalf("makeGroup: %v", err)
	}
	return res.Conversation.ID
}

func asAPIError(t *testing.T, err error) *apierror.Error {
	t.Helper()
	var ae *apierror.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	return ae
}

// --- Send -------------------------------------------------------------

func TestSend_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	got, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "hello world",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.Message.Body != "hello world" {
		t.Errorf("Body = %q", got.Message.Body)
	}
	// last_message_at should bump on the conversation row.
	conv, _ := st.convs.GetConversation(ctx, cid)
	if conv.LastMessageAt.Before(got.Message.CreatedAt.Add(-1)) {
		t.Errorf("last_message_at not bumped: %v vs message %v", conv.LastMessageAt, got.Message.CreatedAt)
	}
}

func TestSend_NonMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	_, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: stranger.ID, Body: "i shouldn't be here",
	})
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestSend_EmptyBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	_, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "   ",
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestSend_OverlongBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	_, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: strings.Repeat("x", 10001),
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

// Regression: when the trimmed view fits but the raw body exceeds
// MaxBodyLen, validation must reject before the DB CHECK does. Earlier
// the rune count was taken AFTER TrimSpace, so a payload like
// strings.Repeat("x", 10000) + " " (10001 raw, 10000 trimmed) would
// pass validation and only blow up at the schema CHECK.
func TestSend_OverlongBodyAfterRawCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	_, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID,
		Body: strings.Repeat("x", message.MaxBodyLen) + " ",
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestSend_RejectsCrossConversationReply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	c1 := makeDirect(ctx, t, st, a, b)
	c2id := makeGroup(ctx, t, st, a, []uuid.UUID{b.ID})

	// Send a message in c1.
	first, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: c1, Sender: a.ID, Body: "first",
	})
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	// Try to reply to it from c2 — must be rejected.
	_, err = st.svc.Send(ctx, message.SendParams{
		ConversationID: c2id, Sender: a.ID, Body: "reply",
		ReplyToMessageID: &first.Message.ID,
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
	}
}

func TestSend_PublishesEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	// Subscribe before sending.
	channel := "conv:" + cid.String() + ":messages"
	ch, err := st.broker.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "watched",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case msg := <-ch:
		if !strings.Contains(string(msg.Payload), "message.created") {
			t.Errorf("payload missing event type: %s", msg.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pubsub event")
	}
}

// --- Edit -------------------------------------------------------------

func TestEdit_OwnerCanEdit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	first, _ := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "first",
	})
	got, err := st.svc.Edit(ctx, message.EditParams{
		Actor: a.ID, MessageID: first.Message.ID, Body: "second",
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if got.Body != "second" {
		t.Errorf("Body = %q", got.Body)
	}
	if got.EditedAt == nil {
		t.Errorf("EditedAt should be set")
	}
}

func TestEdit_NonOwnerForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	first, _ := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "first",
	})
	_, err := st.svc.Edit(ctx, message.EditParams{
		Actor: b.ID, MessageID: first.Message.ID, Body: "hacked",
	})
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q, want FORBIDDEN", asAPIError(t, err).Code)
	}
}

func TestEdit_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.Edit(ctx, message.EditParams{
		Actor: a.ID, MessageID: uuid.New(), Body: "x",
	})
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// --- Delete -----------------------------------------------------------

func TestDelete_OwnerCanDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	first, _ := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "delete me",
	})
	if err := st.svc.Delete(ctx, a.ID, first.Message.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := st.msgs.GetByIDIncludingDeleted(ctx, first.Message.ID)
	if err != nil {
		t.Fatalf("GetByIDIncludingDeleted: %v", err)
	}
	if got.DeletedAt == nil {
		t.Errorf("DeletedAt should be set")
	}
}

func TestDelete_GroupAdminCanDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	creator := makeUser(ctx, t, st)
	other := makeUser(ctx, t, st)
	cid := makeGroup(ctx, t, st, creator, []uuid.UUID{other.ID})
	// `other` sends a message; `creator` (admin) deletes it.
	msg, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: other.ID, Body: "kicked words",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := st.svc.Delete(ctx, creator.ID, msg.Message.ID); err != nil {
		t.Fatalf("Delete by admin: %v", err)
	}
}

func TestDelete_NonOwnerNonAdminForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	creator := makeUser(ctx, t, st)
	other := makeUser(ctx, t, st)
	third := makeUser(ctx, t, st)
	cid := makeGroup(ctx, t, st, creator, []uuid.UUID{other.ID, third.ID})
	msg, _ := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: other.ID, Body: "private",
	})
	err := st.svc.Delete(ctx, third.ID, msg.Message.ID)
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q, want FORBIDDEN", asAPIError(t, err).Code)
	}
}

func TestDelete_StrangerForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	msg, _ := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "private",
	})
	err := st.svc.Delete(ctx, stranger.ID, msg.Message.ID)
	if asAPIError(t, err).Code != apierror.CodeForbidden {
		t.Errorf("Code = %q, want FORBIDDEN", asAPIError(t, err).Code)
	}
}

func TestDelete_AlreadyDeletedIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	msg, _ := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "twice",
	})
	if err := st.svc.Delete(ctx, a.ID, msg.Message.ID); err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	if err := st.svc.Delete(ctx, a.ID, msg.Message.ID); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

// --- List -------------------------------------------------------------

func TestList_OnlyMembersCanRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	for i := 0; i < 3; i++ {
		_, _ = st.svc.Send(ctx, message.SendParams{
			ConversationID: cid, Sender: a.ID, Body: "msg",
		})
	}
	got, err := st.svc.List(ctx, message.ListParams{Actor: a.ID, ConversationID: cid, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Messages) != 3 {
		t.Errorf("len = %d, want 3", len(got.Messages))
	}
	_, err = st.svc.List(ctx, message.ListParams{Actor: stranger.ID, ConversationID: cid, Limit: 10})
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("stranger Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestList_QueryFiltersByText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	_, _ = st.svc.Send(ctx, message.SendParams{ConversationID: cid, Sender: a.ID, Body: "the quick brown fox"})
	_, _ = st.svc.Send(ctx, message.SendParams{ConversationID: cid, Sender: a.ID, Body: "lazy dog rests"})

	got, err := st.svc.List(ctx, message.ListParams{
		Actor: a.ID, ConversationID: cid, Limit: 10, Query: "fox",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Errorf("len = %d, want 1 match", len(got.Messages))
	}
}

// --- MarkRead + ListReads --------------------------------------------

func TestMarkRead_StampsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	msg, _ := st.svc.Send(ctx, message.SendParams{ConversationID: cid, Sender: a.ID, Body: "read me"})

	if err := st.svc.MarkRead(ctx, b.ID, msg.Message.ID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	reads, err := st.svc.ListReads(ctx, a.ID, msg.Message.ID)
	if err != nil {
		t.Fatalf("ListReads: %v", err)
	}
	if len(reads) != 1 || reads[0].UserID != b.ID {
		t.Errorf("reads = %+v, want b's row", reads)
	}
}

func TestMarkRead_NonMemberSeesNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	msg, _ := st.svc.Send(ctx, message.SendParams{ConversationID: cid, Sender: a.ID, Body: "secret"})

	err := st.svc.MarkRead(ctx, stranger.ID, msg.Message.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

func TestListReads_NonMemberSeesNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	stranger := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	msg, _ := st.svc.Send(ctx, message.SendParams{ConversationID: cid, Sender: a.ID, Body: "secret"})

	_, err := st.svc.ListReads(ctx, stranger.ID, msg.Message.ID)
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// --- Config validation ----------------------------------------------

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := message.New(message.Config{}); err == nil {
		t.Error("nil deps should error")
	}
}
