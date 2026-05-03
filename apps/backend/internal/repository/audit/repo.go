// Package audit is the data-access layer for the audit_log table
// (migration 0010). Append-only: §12 admin actions and §8.7
// impersonation bookends call Create; /v1/admin/audit reads via List.
//
// There is no Update or Delete by design — audit rows are evidence,
// not editable state.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// Queries is the per-aggregate repository.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx. Admin actions write
// the audit row inside the same tx as the underlying mutation so a
// half-applied change can't leave a missing log entry.
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// CreateParams is the input to Create. Every field except Action and ID
// is optional — bookend rows often have only an actor.
type CreateParams struct {
	ID         uuid.UUID
	ActorID    *uuid.UUID
	Action     string
	TargetType *string
	TargetID   *uuid.UUID
	Metadata   map[string]any
}

// ListParams is the input to List.
type ListParams struct {
	Cursor *pagination.Cursor
	Limit  int
}

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const createSQL = `-- name: Create :exec
INSERT INTO audit_log (id, actor_id, action, target_type, target_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6)`

const listSQL = `-- name: List :many
SELECT id, actor_id, action, target_type, target_id, metadata, created_at
FROM audit_log
WHERE ($1::timestamptz IS NULL OR (created_at, id) < ($1, $2))
ORDER BY created_at DESC, id DESC
LIMIT $3`

// Create appends one audit_log row. Metadata is JSON-encoded on the
// Go side; passing nil writes SQL NULL (not "null").
func (q *Queries) Create(ctx context.Context, p CreateParams) error {
	if p.Action == "" {
		return errors.New("audit: Create: Action is required")
	}
	if p.ID == uuid.Nil {
		return errors.New("audit: Create: ID is required")
	}
	var metadataBytes []byte
	if p.Metadata != nil {
		raw, err := json.Marshal(p.Metadata)
		if err != nil {
			return fmt.Errorf("audit: marshal metadata: %w", err)
		}
		metadataBytes = raw
	}
	if _, err := q.db.Exec(ctx, createSQL,
		p.ID, p.ActorID, p.Action, p.TargetType, p.TargetID, metadataBytes,
	); err != nil {
		return fmt.Errorf("audit: create: %w", err)
	}
	return nil
}

// List returns audit_log rows newest-first, keyset-paginated on
// (created_at DESC, id DESC). Always over-fetches limit+1 so the
// service layer can compute next_cursor + has_more via pagination.Page.
func (q *Queries) List(ctx context.Context, p ListParams) ([]domain.AuditLog, error) {
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

	rows, err := q.db.Query(ctx, listSQL, ts, id, overFetch)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()

	out := make([]domain.AuditLog, 0, overFetch)
	for rows.Next() {
		row, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("audit: list scan: %w", scanErr)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: list rows: %w", err)
	}
	return out, nil
}

// scanRow decodes one row including the jsonb metadata column.
func scanRow(row pgx.Row) (domain.AuditLog, error) {
	var (
		entry         domain.AuditLog
		metadataBytes []byte
	)
	err := row.Scan(
		&entry.ID,
		&entry.ActorID,
		&entry.Action,
		&entry.TargetType,
		&entry.TargetID,
		&metadataBytes,
		&entry.CreatedAt,
	)
	if err != nil {
		return domain.AuditLog{}, err
	}
	if len(metadataBytes) > 0 {
		if err := json.Unmarshal(metadataBytes, &entry.Metadata); err != nil {
			return domain.AuditLog{}, fmt.Errorf("audit: decode metadata: %w", err)
		}
	}
	return entry, nil
}
