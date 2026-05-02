// Package attachment is the data-access layer for the `attachments`
// table (migration 0006). Read membership semantics live in
// CallerCanRead per §9.3 — same-row check both for linked-to-message
// attachments and for the orphan-during-compose case.
package attachment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is the sentinel returned when a row doesn't exist.
var ErrNotFound = errors.New("attachment: not found")

// Queries is the per-aggregate repository. Goroutine-safe.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx for transactional
// composition (e.g. the orphan sweeper deleting rows after a successful
// S3 object delete).
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const createSQL = `-- name: Create :one
INSERT INTO attachments (id, uploader_id, storage_key, filename, content_type, size_bytes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, uploader_id, storage_key, filename, content_type, size_bytes, created_at`

const getByIDSQL = `-- name: GetByID :one
SELECT id, uploader_id, storage_key, filename, content_type, size_bytes, created_at
FROM attachments
WHERE id = $1`

const listOrphansOlderThanSQL = `-- name: ListOrphansOlderThan :many
SELECT a.id, a.uploader_id, a.storage_key, a.filename, a.content_type, a.size_bytes, a.created_at
FROM attachments a
LEFT JOIN message_attachments ma ON ma.attachment_id = a.id
WHERE ma.attachment_id IS NULL
  AND a.created_at < $1
ORDER BY a.created_at ASC`

const deleteByIDsSQL = `-- name: DeleteByIDs :exec
DELETE FROM attachments
WHERE id = ANY($1::uuid[])`

const callerCanReadSQL = `-- name: CallerCanRead :one
SELECT EXISTS (
    SELECT 1
    FROM attachments a
    JOIN message_attachments ma ON ma.attachment_id = a.id
    JOIN messages m             ON m.id = ma.message_id
    JOIN conversation_members cm ON cm.conversation_id = m.conversation_id
    WHERE a.id = $1 AND cm.user_id = $2
) OR EXISTS (
    SELECT 1
    FROM attachments a
    WHERE a.id = $1
      AND a.uploader_id = $2
      AND NOT EXISTS (SELECT 1 FROM message_attachments ma WHERE ma.attachment_id = a.id)
) AS can_read`

// CreateParams is the input to Create.
type CreateParams struct {
	ID          uuid.UUID
	UploaderID  uuid.UUID
	StorageKey  string
	Filename    string
	ContentType string
	SizeBytes   int64
}

// scanAttachment decodes one row into domain.Attachment. Centralized so
// column order stays consistent.
func scanAttachment(row pgx.Row) (domain.Attachment, error) {
	var a domain.Attachment
	err := row.Scan(
		&a.ID, &a.UploaderID, &a.StorageKey, &a.Filename,
		&a.ContentType, &a.SizeBytes, &a.CreatedAt,
	)
	return a, err
}

// Create inserts a new attachment row. Caller is responsible for first
// uploading the bytes to the object store under p.StorageKey — schema
// has no FK to S3, just the opaque text reference.
func (q *Queries) Create(ctx context.Context, p CreateParams) (domain.Attachment, error) {
	a, err := scanAttachment(q.db.QueryRow(ctx, createSQL,
		p.ID, p.UploaderID, p.StorageKey, p.Filename, p.ContentType, p.SizeBytes))
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("attachment: create: %w", err)
	}
	return a, nil
}

// GetByID returns an attachment by id. Returns ErrNotFound on miss.
func (q *Queries) GetByID(ctx context.Context, id uuid.UUID) (domain.Attachment, error) {
	a, err := scanAttachment(q.db.QueryRow(ctx, getByIDSQL, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Attachment{}, ErrNotFound
	}
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("attachment: get by id: %w", err)
	}
	return a, nil
}

// ListOrphansOlderThan returns attachments older than cutoff that have
// no message_attachments row. Used by the §9.6 orphan sweeper.
func (q *Queries) ListOrphansOlderThan(ctx context.Context, cutoff time.Time) ([]domain.Attachment, error) {
	rows, err := q.db.Query(ctx, listOrphansOlderThanSQL, cutoff)
	if err != nil {
		return nil, fmt.Errorf("attachment: list orphans: %w", err)
	}
	defer rows.Close()
	var out []domain.Attachment
	for rows.Next() {
		a, scanErr := scanAttachment(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("attachment: list orphans scan: %w", scanErr)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attachment: list orphans rows: %w", err)
	}
	return out, nil
}

// DeleteByIDs removes attachment rows by id. The FK on
// message_attachments cascades, so any (race-window) links go with them.
func (q *Queries) DeleteByIDs(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := q.db.Exec(ctx, deleteByIDsSQL, ids); err != nil {
		return fmt.Errorf("attachment: delete by ids: %w", err)
	}
	return nil
}

// CallerCanRead returns true iff the caller can read this attachment
// per §9.3:
//
//   - linked: there exists a message_attachments row whose message is in
//     a conversation the caller is a member of, OR
//   - orphan: zero message_attachments rows AND uploader_id == caller.
//
// One round-trip; service layer just propagates the bool.
func (q *Queries) CallerCanRead(ctx context.Context, attachmentID, userID uuid.UUID) (bool, error) {
	var canRead bool
	if err := q.db.QueryRow(ctx, callerCanReadSQL, attachmentID, userID).Scan(&canRead); err != nil {
		return false, fmt.Errorf("attachment: caller can read: %w", err)
	}
	return canRead, nil
}
