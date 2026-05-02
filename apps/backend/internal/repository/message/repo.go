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
  AND ($5::text = '' OR body_tsv @@ plainto_tsquery('english', $5))
ORDER BY created_at DESC, id DESC
LIMIT $4`

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
