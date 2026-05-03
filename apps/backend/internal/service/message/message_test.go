package message_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	"github.com/cadenlund/wakeup/apps/backend/internal/pushnotif"
	attachrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/attachment"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	msgrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/message"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// fakePresence returns canned []domain.PresenceState filtered to the
// requested IDs — matching the real presence.Service.ListForUsers
// contract. A whole-list fake would let tests pass with mismatched
// IDs and silently drift from production.
//
// If err is non-nil it overrides the data path so the failure branch
// can be exercised.
type fakePresence struct {
	states []domain.PresenceState
	err    error
}

func (f *fakePresence) ListForUsers(_ context.Context, ids []uuid.UUID) ([]domain.PresenceState, error) {
	if f.err != nil {
		return nil, f.err
	}
	byID := make(map[uuid.UUID]domain.PresenceState, len(f.states))
	for _, s := range f.states {
		byID[s.UserID] = s
	}
	out := make([]domain.PresenceState, 0, len(ids))
	for _, id := range ids {
		if s, ok := byID[id]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// fakeNotifier records every SendOfflinePush call. The mutex makes it
// safe under -race, since fanOutOfflinePush runs on the request
// goroutine but presence/notifier are exercised inside Send's caller.
type fakeNotifier struct {
	mu      sync.Mutex
	calls   []notifierCall
	sendErr error
}

type notifierCall struct {
	Recipient uuid.UUID
	Category  notificationpref.Category
	Payload   pushnotif.Notification
}

func (f *fakeNotifier) SendOfflinePush(_ context.Context, recipientID uuid.UUID, category notificationpref.Category, payload pushnotif.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, notifierCall{Recipient: recipientID, Category: category, Payload: payload})
	return f.sendErr
}

func (f *fakeNotifier) snapshot() []notifierCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]notifierCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// stackWithPush builds a stack with stubbed Presence + Notifier so the
// fanOutOfflinePush path runs end-to-end. fakes are returned so tests
// can program them per-case.
func stackWithPush(t *testing.T, presence *fakePresence, notifier *fakeNotifier) *stack {
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
		Presence: presence, Notifications: notifier,
	})
	if err != nil {
		t.Fatalf("message.New: %v", err)
	}
	return &stack{svc: svc, convsvc: cs, convs: convs, msgs: msgs, users: users, pool: pool, broker: broker}
}

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

// Send with attachment IDs links them via AddAttachment in the same
// transaction. Covers the AttachmentIDs-loop body that other Send
// tests skip.
func TestSend_WithAttachments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)

	atts := attachrepo.New(st.pool)
	att1, err := atts.Create(ctx, attachrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), UploaderID: a.ID,
		StorageKey: "k1", Filename: "a.jpg", ContentType: "image/jpeg", SizeBytes: 100,
	})
	if err != nil {
		t.Fatalf("attach Create: %v", err)
	}
	att2, err := atts.Create(ctx, attachrepo.CreateParams{
		ID: uuid.Must(uuid.NewV7()), UploaderID: a.ID,
		StorageKey: "k2", Filename: "b.jpg", ContentType: "image/jpeg", SizeBytes: 200,
	})
	if err != nil {
		t.Fatalf("attach Create 2: %v", err)
	}

	got, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "see attached",
		AttachmentIDs: []uuid.UUID{att1.ID, att2.ID},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(got.Attachments) != 2 {
		t.Errorf("Attachments len = %d, want 2", len(got.Attachments))
	}
}

// Reply target in the same conversation succeeds (covers the
// ConversationID-match path on the reply-validation block).
func TestSend_ReplyToSameConversationSucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	first, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "first",
	})
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	got, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: b.ID, Body: "reply",
		ReplyToMessageID: &first.Message.ID,
	})
	if err != nil {
		t.Fatalf("reply Send: %v", err)
	}
	if got.Message.ReplyToMessageID == nil || *got.Message.ReplyToMessageID != first.Message.ID {
		t.Errorf("reply_to_message_id = %v, want %v", got.Message.ReplyToMessageID, first.Message.ID)
	}
}

// Reply target that doesn't exist returns Validation, not NotFound —
// the field is on the request body, so the failure is shaped as a
// per-field rejection.
func TestSend_ReplyTargetNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	missing := uuid.New()
	_, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "reply",
		ReplyToMessageID: &missing,
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
		if !strings.Contains(string(msg.Payload), "message.new") {
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

// Delete on a non-existent message returns NotFound (covers the
// GetByIDIncludingDeleted ErrNotFound branch on the Delete path).
func TestDelete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	err := st.svc.Delete(ctx, a.ID, uuid.New())
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// Editing a soft-deleted message returns NotFound — UpdateBody filters
// `deleted_at IS NULL`, so it sees zero rows. Covers the
// UpdateBody-ErrNotFound branch on the Edit path.
func TestEdit_OnDeletedMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	first, _ := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "doomed",
	})
	if err := st.svc.Delete(ctx, a.ID, first.Message.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := st.svc.Edit(ctx, message.EditParams{
		Actor: a.ID, MessageID: first.Message.ID, Body: "resurrected",
	})
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// Edit with an empty body short-circuits at validateBody — no DB
// lookup occurs. Covers the validateBody-error early-return on Edit.
func TestEdit_EmptyBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.Edit(ctx, message.EditParams{
		Actor: a.ID, MessageID: uuid.New(), Body: "   ",
	})
	if asAPIError(t, err).Code != apierror.CodeValidation {
		t.Errorf("Code = %q, want VALIDATION_FAILED", asAPIError(t, err).Code)
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

// MarkRead on a non-existent message returns NotFound (covers the
// GetByIDIncludingDeleted ErrNotFound branch).
func TestMarkRead_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	err := st.svc.MarkRead(ctx, a.ID, uuid.New())
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// ListReads on a non-existent message returns NotFound.
func TestListReads_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStack(t)
	a := makeUser(ctx, t, st)
	_, err := st.svc.ListReads(ctx, a.ID, uuid.New())
	if asAPIError(t, err).Code != apierror.CodeNotFound {
		t.Errorf("Code = %q, want RESOURCE_NOT_FOUND", asAPIError(t, err).Code)
	}
}

// Send/Edit/Delete must work when no broker is wired (Config.Broker
// nil) — the publish step becomes a no-op so a fresh test stack
// without WS infra still lets the service round-trip. Covers the
// `s.broker == nil` early-return in publishMessageEvent.
func TestSend_NilBrokerWorks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	convs := convrepo.New(pool)
	msgs := msgrepo.New(pool)
	users := userrepo.New(pool)
	cs, err := convsvc.New(convsvc.Config{Pool: pool, Convs: convs, Users: users})
	if err != nil {
		t.Fatalf("convsvc.New: %v", err)
	}
	svc, err := message.New(message.Config{Pool: pool, Msgs: msgs, Convs: convs})
	if err != nil {
		t.Fatalf("message.New: %v", err)
	}
	st := &stack{svc: svc, convsvc: cs, convs: convs, msgs: msgs, users: users, pool: pool}
	a := makeUser(ctx, t, st)
	b := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, a, b)
	if _, err := svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: a.ID, Body: "no broker, no problem",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// --- Config validation ----------------------------------------------

func TestNew_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := message.New(message.Config{}); err == nil {
		t.Error("nil deps should error")
	}
}

// --- fanOutOfflinePush ----------------------------------------------

// Group conversation: routes via CategoryGroupMessages, skips the
// sender, skips online/away recipients, and pushes to offline ones.
// Asserts the recorded notifier calls match exactly.
func TestSend_FanOut_GroupSkipsSenderAndLive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	notif := &fakeNotifier{}
	presence := &fakePresence{}
	st := stackWithPush(t, presence, notif)

	alice := makeUser(ctx, t, st)
	bob := makeUser(ctx, t, st)
	carol := makeUser(ctx, t, st)
	dave := makeUser(ctx, t, st)

	gid := makeGroup(ctx, t, st, alice, []uuid.UUID{bob.ID, carol.ID, dave.ID})

	// bob online (skip), carol away (skip), dave offline (push). Alice
	// is the sender — never appears in the fan-out regardless of
	// presence.
	presence.states = []domain.PresenceState{
		{UserID: bob.ID, Status: domain.PresenceOnline},
		{UserID: carol.ID, Status: domain.PresenceAway},
		{UserID: dave.ID, Status: domain.PresenceOffline},
	}

	if _, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: gid, Sender: alice.ID, Body: "fan me",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	calls := notif.snapshot()
	if len(calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1; got %#v", len(calls), calls)
	}
	if calls[0].Recipient != dave.ID {
		t.Errorf("recipient = %s, want %s", calls[0].Recipient, dave.ID)
	}
	if calls[0].Category != notificationpref.CategoryGroupMessages {
		t.Errorf("category = %q, want %q", calls[0].Category, notificationpref.CategoryGroupMessages)
	}
	if calls[0].Payload.Body != "fan me" {
		t.Errorf("payload.body = %q, want %q", calls[0].Payload.Body, "fan me")
	}
	if got := calls[0].Payload.Data["conversation_id"]; got != gid.String() {
		t.Errorf("payload.data.conversation_id = %v, want %s", got, gid)
	}
}

// Direct conversation: routes via CategoryDirectMessages.
func TestSend_FanOut_DirectCategory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notif := &fakeNotifier{}
	presence := &fakePresence{}
	st := stackWithPush(t, presence, notif)

	alice := makeUser(ctx, t, st)
	bob := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, alice, bob)

	presence.states = []domain.PresenceState{
		{UserID: bob.ID, Status: domain.PresenceOffline},
	}

	if _, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: alice.ID, Body: "hey",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	calls := notif.snapshot()
	if len(calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1", len(calls))
	}
	if calls[0].Category != notificationpref.CategoryDirectMessages {
		t.Errorf("category = %q, want %q", calls[0].Category, notificationpref.CategoryDirectMessages)
	}
}

// Notifier failure is logged-only — Send still succeeds and the loop
// keeps going across remaining recipients. We verify by sending in a
// group of two offline recipients with notifier returning an error;
// both should still be attempted.
func TestSend_FanOut_NotifierErrorIsBestEffort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notif := &fakeNotifier{sendErr: errors.New("expo down")}
	presence := &fakePresence{}
	st := stackWithPush(t, presence, notif)

	alice := makeUser(ctx, t, st)
	bob := makeUser(ctx, t, st)
	carol := makeUser(ctx, t, st)
	gid := makeGroup(ctx, t, st, alice, []uuid.UUID{bob.ID, carol.ID})

	presence.states = []domain.PresenceState{
		{UserID: bob.ID, Status: domain.PresenceOffline},
		{UserID: carol.ID, Status: domain.PresenceOffline},
	}

	if _, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: gid, Sender: alice.ID, Body: "hey",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := len(notif.snapshot()); got != 2 {
		t.Errorf("notifier attempts = %d, want 2 even when each errored", got)
	}
}

// Presence error short-circuits the fan-out (logged warn) without
// failing Send. Asserts notifier was never called.
func TestSend_FanOut_PresenceErrorSkipsPush(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notif := &fakeNotifier{}
	presence := &fakePresence{err: errors.New("redis down")}
	st := stackWithPush(t, presence, notif)

	alice := makeUser(ctx, t, st)
	bob := makeUser(ctx, t, st)
	cid := makeDirect(ctx, t, st, alice, bob)

	if _, err := st.svc.Send(ctx, message.SendParams{
		ConversationID: cid, Sender: alice.ID, Body: "hey",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := len(notif.snapshot()); got != 0 {
		t.Errorf("notifier called %d times despite presence error", got)
	}
}
