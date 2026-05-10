// Package message is the data-access layer for the messages,
// message_attachments, and message_reads tables (migration 0005).
//
// Soft-delete contract per §4.6: deleted messages stay in the table
// with deleted_at populated. ListByConversation returns them so the
// handler can render the placeholder; GetByID excludes them and
// returns ErrNotFound; GetByIDIncludingDeleted is the escape hatch
// for sender-history rendering.
package message

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is the sentinel returned when a row doesn't exist or is
// soft-deleted (for the queries that filter on deleted_at IS NULL).
var ErrNotFound = errors.New("message: not found")

// Queries is the per-aggregate repository. Goroutine-safe.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx so the message service
// can compose Create + AddAttachment + TouchLastMessageAt atomically.
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const createSQL = `-- name: Create :one
INSERT INTO messages (id, conversation_id, sender_id, body, reply_to_message_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, conversation_id, sender_id, body, reply_to_message_id,
          created_at, edited_at, deleted_at`

const getByIDSQL = `-- name: GetByID :one
SELECT id, conversation_id, sender_id, body, reply_to_message_id,
       created_at, edited_at, deleted_at
FROM messages
WHERE id = $1 AND deleted_at IS NULL`

const getByIDIncludingDeletedSQL = `-- name: GetByIDIncludingDeleted :one
SELECT id, conversation_id, sender_id, body, reply_to_message_id,
       created_at, edited_at, deleted_at
FROM messages
WHERE id = $1`

const updateBodySQL = `-- name: UpdateBody :one
UPDATE messages
SET body = $2, edited_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, conversation_id, sender_id, body, reply_to_message_id,
          created_at, edited_at, deleted_at`

const softDeleteSQL = `-- name: SoftDelete :exec
UPDATE messages
SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL`

const listByConversationSQL = `-- name: ListByConversation :many
SELECT id, conversation_id, sender_id, body, reply_to_message_id,
       created_at, edited_at, deleted_at
FROM messages
WHERE conversation_id = $1
  AND ($2::timestamptz IS NULL OR ($2::timestamptz, $3::uuid) > (created_at, id))
  AND ($5::text = '' OR body ILIKE '%' || $5::text || '%')
ORDER BY created_at DESC, id DESC
LIMIT $4`

// countByConversationSQL mirrors listByConversationSQL minus the
// keyset cursor + LIMIT — returns the absolute number of
// messages in the conversation that match an optional body
// substring filter. Drives the thread-screen scroll-to hints.
//
// Includes soft-deleted rows so the total matches the list slice
// (which renders deleted rows as the §4.6 placeholder). Otherwise
// "Showing N of M" would lie when the conversation has any
// tombstones — the slice would have N visible rows but `total`
// would only count the survivors (CodeRabbit on PR #138).
const countByConversationSQL = `-- name: CountByConversation :one
SELECT COUNT(*)
FROM messages
WHERE conversation_id = $1
  AND ($2::text = '' OR body ILIKE '%' || $2::text || '%')`

const addAttachmentSQL = `-- name: AddAttachment :exec
INSERT INTO message_attachments (message_id, attachment_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING`

const listAttachmentsForMessageSQL = `-- name: ListAttachmentsForMessage :many
SELECT attachment_id
FROM message_attachments
WHERE message_id = $1
ORDER BY attachment_id`

const markReadSQL = `-- name: MarkRead :exec
INSERT INTO message_reads (message_id, user_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING`

const listReadsForMessageSQL = `-- name: ListReadsForMessage :many
SELECT message_id, user_id, read_at
FROM message_reads
WHERE message_id = $1
ORDER BY read_at DESC, user_id`

// searchInUserConversationsSQL: same direct-conv block-filter as
// listConversationsByUserSQL — a message that lives in a direct
// conversation whose other member is in a 'blocked' edge with the
// caller is hidden from search results. Group conversations are
// untouched (Phase 6's thread surface hides blocked-sender bubbles
// per-message). Without this, /v1/search?types=messages leaked
// blocked DM bodies even though the conversation itself was hidden
// from /v1/conversations.
const searchInUserConversationsSQL = `-- name: SearchInUserConversations :many
SELECT m.id, m.conversation_id, m.sender_id, m.body, m.reply_to_message_id,
       m.created_at, m.edited_at, m.deleted_at
FROM messages m
JOIN conversation_members cm ON cm.conversation_id = m.conversation_id AND cm.user_id = $1
JOIN conversations c ON c.id = m.conversation_id
WHERE m.deleted_at IS NULL
  AND m.body ILIKE '%' || $2::text || '%'
  AND (
    c.type <> 'direct'
    OR NOT EXISTS (
      SELECT 1
      FROM conversation_members other
      WHERE other.conversation_id = c.id
        AND other.user_id <> $1
        AND EXISTS (
          SELECT 1 FROM friendships f
          WHERE f.status = 'blocked'
            AND ((f.requester_id = $1 AND f.addressee_id = other.user_id)
              OR (f.requester_id = other.user_id AND f.addressee_id = $1))
        )
    )
  )
ORDER BY m.created_at DESC, m.id DESC
LIMIT $3`

// countSearchInUserConversationsSQL mirrors searchInUserConversationsSQL
// without the LIMIT — returns the absolute number of matching
// messages across every page. Drives the "showing 10 of N"
// hint on the search modal.
const countSearchInUserConversationsSQL = `-- name: CountSearchInUserConversations :one
SELECT COUNT(*)
FROM messages m
JOIN conversation_members cm ON cm.conversation_id = m.conversation_id AND cm.user_id = $1
JOIN conversations c ON c.id = m.conversation_id
WHERE m.deleted_at IS NULL
  AND m.body ILIKE '%' || $2::text || '%'
  AND (
    c.type <> 'direct'
    OR NOT EXISTS (
      SELECT 1
      FROM conversation_members other
      WHERE other.conversation_id = c.id
        AND other.user_id <> $1
        AND EXISTS (
          SELECT 1 FROM friendships f
          WHERE f.status = 'blocked'
            AND ((f.requester_id = $1 AND f.addressee_id = other.user_id)
              OR (f.requester_id = other.user_id AND f.addressee_id = $1))
        )
    )
  )`

const countUnreadForUserSQL = `-- name: CountUnreadForUser :one
WITH last_read AS (
    SELECT cm.conversation_id,
           cm.user_id,
           lr.created_at AS last_read_at
    FROM conversation_members cm
    LEFT JOIN messages lr ON lr.id = cm.last_read_message_id
    WHERE cm.user_id = $1
)
SELECT COUNT(*)::bigint
FROM messages m
JOIN last_read r ON r.conversation_id = m.conversation_id
WHERE m.sender_id <> $1
  AND m.deleted_at IS NULL
  AND (r.last_read_at IS NULL OR m.created_at > r.last_read_at)`

// CreateParams is the input to Create.
type CreateParams struct {
	ID               uuid.UUID
	ConversationID   uuid.UUID
	SenderID         uuid.UUID
	Body             string
	ReplyToMessageID *uuid.UUID
}

// scanMessage decodes one row into domain.Message. Centralized so column
// order stays consistent across queries.
func scanMessage(row pgx.Row) (domain.Message, error) {
	var m domain.Message
	err := row.Scan(
		&m.ID, &m.ConversationID, &m.SenderID, &m.Body, &m.ReplyToMessageID,
		&m.CreatedAt, &m.EditedAt, &m.DeletedAt,
	)
	return m, err
}

// Create inserts a new message row. The body length CHECK + the
// conversation_id FK are enforced at the schema level.
func (q *Queries) Create(ctx context.Context, p CreateParams) (domain.Message, error) {
	m, err := scanMessage(q.db.QueryRow(ctx, createSQL,
		p.ID, p.ConversationID, p.SenderID, p.Body, p.ReplyToMessageID))
	if err != nil {
		return domain.Message{}, fmt.Errorf("message: create: %w", err)
	}
	return m, nil
}

// GetByID returns the message (excluding soft-deleted). Service-layer
// reads use this so deleted bodies don't leak.
func (q *Queries) GetByID(ctx context.Context, id uuid.UUID) (domain.Message, error) {
	m, err := scanMessage(q.db.QueryRow(ctx, getByIDSQL, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Message{}, ErrNotFound
	}
	if err != nil {
		return domain.Message{}, fmt.Errorf("message: get by id: %w", err)
	}
	return m, nil
}

// GetByIDIncludingDeleted returns the message even when soft-deleted.
// Used by handlers that render historical sender info on the §4.6
// placeholder.
func (q *Queries) GetByIDIncludingDeleted(ctx context.Context, id uuid.UUID) (domain.Message, error) {
	m, err := scanMessage(q.db.QueryRow(ctx, getByIDIncludingDeletedSQL, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Message{}, ErrNotFound
	}
	if err != nil {
		return domain.Message{}, fmt.Errorf("message: get by id incl deleted: %w", err)
	}
	return m, nil
}

// UpdateBody replaces the body of a non-deleted message. Returns
// ErrNotFound on missing or already-deleted rows.
func (q *Queries) UpdateBody(ctx context.Context, id uuid.UUID, body string) (domain.Message, error) {
	m, err := scanMessage(q.db.QueryRow(ctx, updateBodySQL, id, body))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Message{}, ErrNotFound
	}
	if err != nil {
		return domain.Message{}, fmt.Errorf("message: update body: %w", err)
	}
	return m, nil
}

// SoftDelete stamps deleted_at. Idempotent — repeated calls are no-ops
// after the first.
func (q *Queries) SoftDelete(ctx context.Context, id uuid.UUID) error {
	if _, err := q.db.Exec(ctx, softDeleteSQL, id); err != nil {
		return fmt.Errorf("message: soft delete: %w", err)
	}
	return nil
}

// ListByConversationParams is the input to ListByConversation.
type ListByConversationParams struct {
	ConversationID uuid.UUID
	Cursor         *pagination.Cursor
	Limit          int
	// Query is the optional full-text search term. Empty disables the
	// tsvector filter; non-empty applies plainto_tsquery('english', q).
	Query string
}

// ListByConversation returns the conversation's messages keyset-paginated
// on (created_at DESC, id DESC). Soft-deleted rows are INCLUDED so the
// handler can render placeholders.
//
// Always over-fetches limit+1 so the service layer can call
// pagination.Page to compute next_cursor + has_more.
func (q *Queries) ListByConversation(ctx context.Context, p ListByConversationParams) ([]domain.Message, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = pagination.DefaultLimit
	}
	overFetch := limit + 1

	var ts *time.Time
	var id *uuid.UUID
	if p.Cursor != nil {
		ts = &p.Cursor.Timestamp
		id = &p.Cursor.ID
	}

	rows, err := q.db.Query(ctx, listByConversationSQL,
		p.ConversationID, ts, id, overFetch, strings.TrimSpace(p.Query))
	if err != nil {
		return nil, fmt.Errorf("message: list by conversation: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Message, 0, overFetch)
	for rows.Next() {
		m, scanErr := scanMessage(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("message: list by conversation scan: %w", scanErr)
		}
		out = append(out, m)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("message: list by conversation rows: %w", rowsErr)
	}
	return out, nil
}

// CountByConversation returns the absolute number of non-deleted
// messages in the conversation matching the optional body
// substring. Drives the thread-screen "X of N" total. Pass an
// empty query to count every message.
func (q *Queries) CountByConversation(ctx context.Context, conversationID uuid.UUID, query string) (int, error) {
	var n int
	if err := q.db.QueryRow(ctx, countByConversationSQL, conversationID, strings.TrimSpace(query)).Scan(&n); err != nil {
		return 0, fmt.Errorf("message: count by conversation: %w", err)
	}
	return n, nil
}

// AddAttachment links an attachment to a message. Idempotent — a
// re-link is a no-op (PK collision is swallowed).
func (q *Queries) AddAttachment(ctx context.Context, messageID, attachmentID uuid.UUID) error {
	if _, err := q.db.Exec(ctx, addAttachmentSQL, messageID, attachmentID); err != nil {
		return fmt.Errorf("message: add attachment: %w", err)
	}
	return nil
}

// ListAttachmentsForMessage returns the attachment IDs linked to a
// message. Order is by attachment_id (UUID v7 → newest first when ids
// are time-sortable).
func (q *Queries) ListAttachmentsForMessage(ctx context.Context, messageID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.db.Query(ctx, listAttachmentsForMessageSQL, messageID)
	if err != nil {
		return nil, fmt.Errorf("message: list attachments: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("message: list attachments scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("message: list attachments rows: %w", err)
	}
	return out, nil
}

// MarkRead stamps a (message_id, user_id) read row. Idempotent — first
// read_at wins. Service layer is responsible for membership checks.
func (q *Queries) MarkRead(ctx context.Context, messageID, userID uuid.UUID) error {
	if _, err := q.db.Exec(ctx, markReadSQL, messageID, userID); err != nil {
		return fmt.Errorf("message: mark read: %w", err)
	}
	return nil
}

// ListReadsForMessage returns the read receipts for a message,
// newest-first. Used by GET /v1/messages/{id}/reads.
func (q *Queries) ListReadsForMessage(ctx context.Context, messageID uuid.UUID) ([]domain.MessageRead, error) {
	rows, err := q.db.Query(ctx, listReadsForMessageSQL, messageID)
	if err != nil {
		return nil, fmt.Errorf("message: list reads: %w", err)
	}
	defer rows.Close()
	var out []domain.MessageRead
	for rows.Next() {
		var r domain.MessageRead
		if err := rows.Scan(&r.MessageID, &r.UserID, &r.ReadAt); err != nil {
			return nil, fmt.Errorf("message: list reads scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("message: list reads rows: %w", err)
	}
	return out, nil
}

// SearchInUserConversations runs a cross-conversation full-text search
// restricted to conversations userID is a member of. limit caps the
// result set so the unified search handler renders fast (recommended
// 10-25 per request). Soft-deleted messages excluded.
func (q *Queries) SearchInUserConversations(ctx context.Context, userID uuid.UUID, query string, limit int) ([]domain.Message, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := q.db.Query(ctx, searchInUserConversationsSQL, userID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("message: search in user convs: %w", err)
	}
	defer rows.Close()
	var out []domain.Message
	for rows.Next() {
		m, scanErr := scanMessage(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("message: search scan: %w", scanErr)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("message: search rows: %w", err)
	}
	return out, nil
}

// CountSearchInUserConversations returns the absolute count of
// matching messages for the same WHERE clause as
// SearchInUserConversations — what the UI uses for the
// "showing 10 of N" hint on the search modal.
func (q *Queries) CountSearchInUserConversations(ctx context.Context, userID uuid.UUID, query string) (int, error) {
	var n int
	if err := q.db.QueryRow(ctx, countSearchInUserConversationsSQL, userID, query).Scan(&n); err != nil {
		return 0, fmt.Errorf("message: count search in user convs: %w", err)
	}
	return n, nil
}

// CountUnreadForUser returns the total number of unread messages
// across every conversation userID is a member of. "Unread" excludes
// messages userID authored, soft-deleted messages, and anything sent
// at or before userID's last_read_message_id row's created_at.
//
// Surfaces the X-Unread-Total header on GET /v1/auth/me and the
// `unread_total` field on the WS heartbeat (WAKEUPEXPO.md §7.5 badge).
func (q *Queries) CountUnreadForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	if err := q.db.QueryRow(ctx, countUnreadForUserSQL, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("message: count unread: %w", err)
	}
	return n, nil
}
