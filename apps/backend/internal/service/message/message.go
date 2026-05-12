// Package message is the service layer for messages, attachments link,
// and read receipts. Composes the message + conversation repos plus a
// pubsub broker so each Send / Edit / Delete fans out to whatever
// websocket subscribers are listening on the conversation channel
// (§4.5 — `conv:<id>:messages`).
//
// Send is transactional: Create + AddAttachment(s) + TouchLastMessageAt
// commit together so a partial failure can't leave a message without
// its attachment link or with a stale conversation timestamp. The
// pubsub publish runs OUTSIDE the transaction so a broker outage can't
// roll back a successful insert.
package message

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	"github.com/cadenlund/wakeup/apps/backend/internal/pushnotif"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	msgrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// MaxBodyLen mirrors §4.6 — schema CHECK enforces it too. Service-layer
// guard avoids hitting the DB on a known-bad input.
const MaxBodyLen = 10000

// PresenceLister is the slice of presence.Service this package needs to
// decide whether a recipient is "live" (connected via WS) or offline (push
// candidate). Defining the interface here keeps the message package free
// of a hard dep on the presence service.
type PresenceLister interface {
	ListForUsers(ctx context.Context, ids []uuid.UUID) ([]domain.PresenceState, error)
}

// OfflinePusher is the slice of notification.Service this package needs.
// Same shape as notification.Service.SendOfflinePush so the harness wires
// the real service in directly; tests can stub. The trailing *uuid.UUID
// is the conversation scope — message pushes always pass &convID so
// per-conv mute (§10.2) gates delivery.
type OfflinePusher interface {
	SendOfflinePush(ctx context.Context, recipientID uuid.UUID, category notificationpref.Category, payload pushnotif.Notification, convID *uuid.UUID) error
}

// Service is the message service.
type Service struct {
	pool          *pgxpool.Pool // for the Send transaction
	msgs          *msgrepo.Queries
	convs         *convrepo.Queries
	broker        pubsub.Broker
	presence      PresenceLister
	notifications OfflinePusher
	logger        *slog.Logger
}

// Config builds the service. Broker may be nil — in that case Send /
// Edit / Delete still succeed; pubsub publish becomes a no-op (useful
// in tests that don't care about WS fan-out).
//
// Presence + Notifications are also optional: when either is nil, the
// §11.5 offline-push fan-out becomes a no-op. Production wires both;
// repo-level tests skip them.
type Config struct {
	Pool          *pgxpool.Pool
	Msgs          *msgrepo.Queries
	Convs         *convrepo.Queries
	Broker        pubsub.Broker
	Presence      PresenceLister
	Notifications OfflinePusher
	Logger        *slog.Logger
}

// New constructs the service.
func New(cfg Config) (*Service, error) {
	if cfg.Pool == nil {
		return nil, errors.New("message: Config.Pool is required")
	}
	if cfg.Msgs == nil {
		return nil, errors.New("message: Config.Msgs is required")
	}
	if cfg.Convs == nil {
		return nil, errors.New("message: Config.Convs is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		pool: cfg.Pool, msgs: cfg.Msgs, convs: cfg.Convs,
		broker:        cfg.Broker,
		presence:      cfg.Presence,
		notifications: cfg.Notifications,
		logger:        logger,
	}, nil
}

// SendParams is the input to Send.
type SendParams struct {
	ConversationID   uuid.UUID
	Sender           uuid.UUID
	Body             string
	AttachmentIDs    []uuid.UUID
	ReplyToMessageID *uuid.UUID
}

// SendResult is what callers need after a successful Send.
type SendResult struct {
	Message     domain.Message
	Attachments []uuid.UUID
}

// Send creates a new message in a conversation.
//
// Atomicity: Create + AddAttachment(s) + TouchLastMessageAt run inside
// one transaction. If any step fails, the whole thing rolls back —
// no half-created message, no stale last_message_at.
func (s *Service) Send(ctx context.Context, p SendParams) (SendResult, error) {
	if err := validateBody(p.Body); err != nil {
		return SendResult{}, err
	}
	if _, err := s.convs.GetMember(ctx, p.ConversationID, p.Sender); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return SendResult{}, apierror.NotFound("conversation")
		}
		return SendResult{}, apierror.Internal("get member").WithCause(err)
	}
	if p.ReplyToMessageID != nil {
		// Validate the reply target exists and belongs to the same
		// conversation. Crossing conversations would be a data leak.
		ref, err := s.msgs.GetByID(ctx, *p.ReplyToMessageID)
		if err != nil {
			if errors.Is(err, msgrepo.ErrNotFound) {
				return SendResult{}, apierror.Validation([]apierror.FieldError{{
					Field: "reply_to_message_id", Code: "INVALID_VALUE",
					Message: "reply target not found",
				}})
			}
			return SendResult{}, apierror.Internal("get reply target").WithCause(err)
		}
		if ref.ConversationID != p.ConversationID {
			return SendResult{}, apierror.Validation([]apierror.FieldError{{
				Field: "reply_to_message_id", Code: "INVALID_VALUE",
				Message: "reply target is in a different conversation",
			}})
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SendResult{}, apierror.Internal("begin tx").WithCause(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	msgs := s.msgs.WithTx(tx)
	convs := s.convs.WithTx(tx)

	id, err := uuid.NewV7()
	if err != nil {
		return SendResult{}, apierror.Internal("uuid").WithCause(err)
	}
	created, err := msgs.Create(ctx, msgrepo.CreateParams{
		ID: id, ConversationID: p.ConversationID, SenderID: p.Sender,
		Body: p.Body, ReplyToMessageID: p.ReplyToMessageID,
	})
	if err != nil {
		return SendResult{}, apierror.Internal("create message").WithCause(err)
	}
	for _, attID := range p.AttachmentIDs {
		if err := msgs.AddAttachment(ctx, created.ID, attID); err != nil {
			return SendResult{}, apierror.Internal("add attachment").WithCause(err)
		}
	}
	if err := convs.TouchLastMessageAt(ctx, p.ConversationID, created.CreatedAt); err != nil {
		return SendResult{}, apierror.Internal("touch last_message_at").WithCause(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SendResult{}, apierror.Internal("commit").WithCause(err)
	}

	s.publishMessageEvent(ctx, wsproto.EventMessageNew, created)
	s.fanOutOfflinePush(ctx, p.ConversationID, created, p.Body)
	return SendResult{Message: created, Attachments: append([]uuid.UUID(nil), p.AttachmentIDs...)}, nil
}

// fanOutOfflinePush sends an Expo push to every conversation member who
// (a) isn't the sender and (b) doesn't have a live WS connection
// (presence != online and != away). Category routes by conversation
// type per §11.5: direct → direct_messages, group → group_messages.
//
// Errors are logged at warn level and never bubble up — push is a
// best-effort side channel; a Redis or Expo blip mustn't undo a
// successful Send.
func (s *Service) fanOutOfflinePush(ctx context.Context, convID uuid.UUID, created domain.Message, originalBody string) {
	if s.presence == nil || s.notifications == nil {
		return
	}
	conv, err := s.convs.GetConversation(ctx, convID)
	if err != nil {
		s.logger.Warn("message: offline-push: get conversation",
			slog.String("conversation_id", convID.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	members, err := s.convs.ListMembers(ctx, convID)
	if err != nil {
		s.logger.Warn("message: offline-push: list members",
			slog.String("conversation_id", convID.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	recipients := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		if m.UserID != created.SenderID {
			recipients = append(recipients, m.UserID)
		}
	}
	if len(recipients) == 0 {
		return
	}
	presences, err := s.presence.ListForUsers(ctx, recipients)
	if err != nil {
		s.logger.Warn("message: offline-push: list presence",
			slog.String("conversation_id", convID.String()),
			slog.String("error", err.Error()),
		)
		return
	}

	category := notificationpref.CategoryDirectMessages
	if conv.Type == domain.ConversationGroup {
		category = notificationpref.CategoryGroupMessages
	}
	payload := pushnotif.Notification{
		Title: "New message",
		Body:  bodyPreview(originalBody),
		Data: map[string]any{
			"type":            "message",
			"conversation_id": convID.String(),
			"message_id":      created.ID.String(),
		},
	}
	for _, ps := range presences {
		if ps.Status == domain.PresenceOnline || ps.Status == domain.PresenceAway {
			continue
		}
		if err := s.notifications.SendOfflinePush(ctx, ps.UserID, category, payload, &convID); err != nil {
			s.logger.Warn("message: offline-push: send",
				slog.String("recipient_id", ps.UserID.String()),
				slog.String("category", string(category)),
				slog.String("error", err.Error()),
			)
		}
	}
}

// bodyPreview returns the first ~100 runes of body, falling back to a
// generic "You have a new message" when body is empty (attachment-only
// messages). Push payload Body is shown in the OS toast — keep it short.
func bodyPreview(body string) string {
	const maxRunes = 100
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "You have a new message"
	}
	runes := []rune(trimmed)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return string(runes)
}

// EditParams is the input to Edit.
type EditParams struct {
	Actor     uuid.UUID
	MessageID uuid.UUID
	Body      string
}

// Edit updates the body of an existing message. Owner-only; Forbidden
// otherwise. Refuses on already-deleted rows.
func (s *Service) Edit(ctx context.Context, p EditParams) (domain.Message, error) {
	if err := validateBody(p.Body); err != nil {
		return domain.Message{}, err
	}
	current, err := s.msgs.GetByID(ctx, p.MessageID)
	if err != nil {
		if errors.Is(err, msgrepo.ErrNotFound) {
			return domain.Message{}, apierror.NotFound("message")
		}
		return domain.Message{}, apierror.Internal("get message").WithCause(err)
	}
	if current.SenderID != p.Actor {
		return domain.Message{}, apierror.Forbidden("only the sender can edit this message")
	}
	updated, err := s.msgs.UpdateBody(ctx, p.MessageID, p.Body)
	if err != nil {
		if errors.Is(err, msgrepo.ErrNotFound) {
			return domain.Message{}, apierror.NotFound("message")
		}
		return domain.Message{}, apierror.Internal("update body").WithCause(err)
	}
	s.publishMessageEvent(ctx, wsproto.EventMessageEdited, updated)
	return updated, nil
}

// Delete soft-deletes a message. Permitted to:
//   - the sender, OR
//   - an admin of the conversation
//
// Idempotent — re-deleting a soft-deleted row is a no-op.
func (s *Service) Delete(ctx context.Context, actor, messageID uuid.UUID) error {
	current, err := s.msgs.GetByIDIncludingDeleted(ctx, messageID)
	if err != nil {
		if errors.Is(err, msgrepo.ErrNotFound) {
			return apierror.NotFound("message")
		}
		return apierror.Internal("get message").WithCause(err)
	}
	if current.IsDeleted() {
		// Already deleted — idempotent success.
		return nil
	}
	if current.SenderID != actor {
		// Maybe a conversation admin: look up actor's role.
		member, err := s.convs.GetMember(ctx, current.ConversationID, actor)
		if err != nil {
			if errors.Is(err, convrepo.ErrNotFound) {
				return apierror.Forbidden("only the sender or a group admin can delete this message")
			}
			return apierror.Internal("get member").WithCause(err)
		}
		if !member.IsAdmin() {
			return apierror.Forbidden("only the sender or a group admin can delete this message")
		}
	}
	if err := s.msgs.SoftDelete(ctx, messageID); err != nil {
		return apierror.Internal("soft delete").WithCause(err)
	}
	s.publishMessageEvent(ctx, wsproto.EventMessageDeleted, current)
	return nil
}

// ListParams is the input to List.
type ListParams struct {
	Actor          uuid.UUID
	ConversationID uuid.UUID
	Cursor         *pagination.Cursor
	Limit          int
	Query          string
}

// ListResult is the paginated payload returned by List.
// Total is the absolute count of matching messages across every page so
// the UI can render "showing N of M" hints without paginating through
// every cursor. Soft-deleted rows are INCLUDED in Total so the count
// matches the list slice (which renders deleted rows as the §4.6
// placeholder); if we excluded tombstones from Total, "Showing N of M"
// would read N > M whenever the conversation had any deletions.
type ListResult struct {
	Messages   []domain.Message
	Total      int
	NextCursor *string
	HasMore    bool
}

// List returns messages from a conversation the caller is a member of.
// Soft-deleted rows are included so the §4.6 placeholder can render —
// the handler-side DTO converter blanks the body.
func (s *Service) List(ctx context.Context, p ListParams) (ListResult, error) {
	if _, err := s.convs.GetMember(ctx, p.ConversationID, p.Actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return ListResult{}, apierror.NotFound("conversation")
		}
		return ListResult{}, apierror.Internal("get member").WithCause(err)
	}
	overFetched, err := s.msgs.ListByConversation(ctx, msgrepo.ListByConversationParams{
		ConversationID: p.ConversationID, Cursor: p.Cursor, Limit: p.Limit, Query: p.Query,
	})
	if err != nil {
		return ListResult{}, apierror.Internal("list messages").WithCause(err)
	}
	total, err := s.msgs.CountByConversation(ctx, p.ConversationID, p.Query)
	if err != nil {
		return ListResult{}, apierror.Internal("count messages").WithCause(err)
	}
	data, next, hasMore := pagination.Page(overFetched, p.Limit, func(m domain.Message) pagination.Cursor {
		return pagination.Cursor{Timestamp: m.CreatedAt, ID: m.ID}
	})
	return ListResult{Messages: data, Total: total, NextCursor: next, HasMore: hasMore}, nil
}

// MarkRead stamps a (message_id, user_id) read row. Caller must be a
// member of the message's conversation.
func (s *Service) MarkRead(ctx context.Context, actor, messageID uuid.UUID) error {
	m, err := s.msgs.GetByIDIncludingDeleted(ctx, messageID)
	if err != nil {
		if errors.Is(err, msgrepo.ErrNotFound) {
			return apierror.NotFound("message")
		}
		return apierror.Internal("get message").WithCause(err)
	}
	if _, err := s.convs.GetMember(ctx, m.ConversationID, actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return apierror.NotFound("message")
		}
		return apierror.Internal("get member").WithCause(err)
	}
	if err := s.msgs.MarkRead(ctx, messageID, actor); err != nil {
		return apierror.Internal("mark read").WithCause(err)
	}
	return nil
}

// ListReads returns who has read the message. Caller must be a member
// of the message's conversation.
func (s *Service) ListReads(ctx context.Context, actor, messageID uuid.UUID) ([]domain.MessageRead, error) {
	m, err := s.msgs.GetByIDIncludingDeleted(ctx, messageID)
	if err != nil {
		if errors.Is(err, msgrepo.ErrNotFound) {
			return nil, apierror.NotFound("message")
		}
		return nil, apierror.Internal("get message").WithCause(err)
	}
	if _, err := s.convs.GetMember(ctx, m.ConversationID, actor); err != nil {
		if errors.Is(err, convrepo.ErrNotFound) {
			return nil, apierror.NotFound("message")
		}
		return nil, apierror.Internal("get member").WithCause(err)
	}
	reads, err := s.msgs.ListReadsForMessage(ctx, messageID)
	if err != nil {
		return nil, apierror.Internal("list reads").WithCause(err)
	}
	return reads, nil
}

// CountUnreadByConversation returns the per-conversation unread count
// for userID across the given conversations, keyed by conversation ID.
// "Unread" excludes messages userID authored, soft-deleted messages,
// and anything at or before userID's read pointer. Conversations with
// zero unread are omitted from the map — callers treat a missing key
// as 0. Surfaces the `unread_count` field on each ConversationResponse.
func (s *Service) CountUnreadByConversation(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	counts, err := s.msgs.CountUnreadByConversation(ctx, userID, convIDs)
	if err != nil {
		return nil, apierror.Internal("count unread by conversation").WithCause(err)
	}
	return counts, nil
}

// LatestMessageByConversation returns the most recent message in each of
// the given conversations, keyed by conversation ID (soft-deleted
// messages included — see the repo method). Surfaces the `last_message`
// preview on each ConversationResponse.
func (s *Service) LatestMessageByConversation(ctx context.Context, convIDs []uuid.UUID) (map[uuid.UUID]domain.Message, error) {
	latest, err := s.msgs.LatestMessageByConversation(ctx, convIDs)
	if err != nil {
		return nil, apierror.Internal("latest message by conversation").WithCause(err)
	}
	return latest, nil
}

// publishMessageEvent fires-and-forgets a pubsub event on the
// `conv:<id>:messages` channel. The broker is optional — when nil
// (e.g. tests that don't care about WS fan-out), this is a no-op.
// Errors are logged at debug level: a broker outage can't undo an
// already-committed insert.
//
// `message.new` / `message.edited` carry MessageEventPayload (ids +
// `body` for the in-app banner preview); `message.deleted` carries
// the leaner MessageDeletedPayload — there's nothing left to preview,
// and re-broadcasting the just-deleted text would be needless.
//
// Wire shape is the §7.1 wsproto envelope so the WS bridge can fan
// the bytes straight to clients without re-wrapping. Earlier code
// here published a flat map under the type name "message.created",
// which mismatched both the §7.2 event registry ("message.new") and
// the §7.1 wire format — caught when matrix tests landed in 8.4.
func (s *Service) publishMessageEvent(ctx context.Context, eventType wsproto.EventType, m domain.Message) {
	if s.broker == nil {
		return
	}
	var data any = wsproto.MessageEventPayload{
		MessageID:      m.ID,
		ConversationID: m.ConversationID,
		SenderID:       m.SenderID,
		CreatedAt:      m.CreatedAt,
		Body:           m.Body,
	}
	if eventType == wsproto.EventMessageDeleted {
		data = wsproto.MessageDeletedPayload{MessageID: m.ID, ConversationID: m.ConversationID}
	}
	payload, err := wsproto.Encode(eventType, data)
	if err != nil {
		s.logger.Warn("message: encode event", slog.String("error", err.Error()))
		return
	}
	channel := fmt.Sprintf("conv:%s:messages", m.ConversationID)
	if err := s.broker.Publish(ctx, channel, payload); err != nil {
		s.logger.Warn("message: publish event",
			slog.String("channel", channel),
			slog.String("error", err.Error()),
		)
	}
}

// validateBody enforces the §4.6 1-10000 rune-count rule. The schema
// CHECK is char_length-based (which counts code points, same as Go
// `len([]rune)`); doing the same check here gives a friendlier error
// without a DB round-trip.
//
// Length is measured against the ORIGINAL body, not the trimmed view —
// the DB stores what the caller sent, and the schema CHECK runs against
// that same untrimmed value. Trimming is only used to detect the
// whitespace-only case so we surface REQUIRED instead of letting the
// row insert succeed.
func validateBody(body string) error {
	if strings.TrimSpace(body) == "" {
		return apierror.Validation([]apierror.FieldError{{
			Field: "body", Code: "REQUIRED",
			Message: "message body is required",
		}})
	}
	// Postgres char_length counts code points; we mirror with rune count.
	runes := 0
	for range body {
		runes++
	}
	if runes > MaxBodyLen {
		return apierror.Validation([]apierror.FieldError{{
			Field: "body", Code: "TOO_LONG",
			Message: fmt.Sprintf("message body must be at most %d characters", MaxBodyLen),
		}})
	}
	return nil
}
