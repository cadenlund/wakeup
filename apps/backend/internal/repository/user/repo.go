// Package user is the data-access layer for the users table (migration
// 0001). Returns domain.User; never raw pgx rows. Service-level concerns
// (password hashing, validator-tagged input) live above this package.
package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is the sentinel returned when a row doesn't exist (or is
// soft-deleted, for queries that filter on deleted_at IS NULL). Callers
// compare with errors.Is.
var ErrNotFound = errors.New("user: not found")

// Queries is the per-aggregate repository. Goroutine-safe; cheap to copy.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx so a service can call
// several repos atomically (§4.2).
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const createSQL = `-- name: Create :one
INSERT INTO users (id, username, display_name, email, password_hash)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at`

const getByIDSQL = `-- name: GetByID :one
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE id = $1 AND deleted_at IS NULL`

const getByIDIncludingDeletedSQL = `-- name: GetByIDIncludingDeleted :one
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE id = $1`

const getByUsernameSQL = `-- name: GetByUsername :one
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE username = $1 AND deleted_at IS NULL`

const getByEmailSQL = `-- name: GetByEmail :one
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE email = $1 AND deleted_at IS NULL`

const updateSQL = `-- name: Update :one
UPDATE users
SET display_name = COALESCE($2, display_name),
    avatar_url   = COALESCE($3, avatar_url),
    color_scheme = COALESCE($4, color_scheme)
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at`

const updatePasswordSQL = `-- name: UpdatePassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1 AND deleted_at IS NULL`

const updateRoleSQL = `-- name: UpdateRole :exec
UPDATE users SET role = $2 WHERE id = $1 AND deleted_at IS NULL`

const softDeleteSQL = `-- name: SoftDelete :exec
UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`

const listByPrefixSQL = `-- name: ListByPrefix :many
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE deleted_at IS NULL
  AND (
    $1::text = ''
    OR username ILIKE $1::text || '%'
    OR display_name ILIKE $1::text || '%'
  )
  AND ($2::timestamptz IS NULL OR (created_at, id) < ($2::timestamptz, $3::uuid))
ORDER BY created_at DESC, id DESC
LIMIT $4`

const listByIDsSQL = `-- name: ListByIDs :many
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE id = ANY($1::uuid[])`

// scanUser decodes one row into domain.User. Centralized so every method
// uses the same column order — keeps the row-shape promise consistent.
func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(
		&u.ID,
		&u.Username,
		&u.DisplayName,
		&u.Email,
		&u.PasswordHash,
		&u.AvatarURL,
		&u.ColorScheme,
		&u.Role,
		&u.CreatedAt,
		&u.UpdatedAt,
		&u.DeletedAt,
	)
	return u, err
}

// Create inserts a user. Conflicts on the unique (username, email) indexes
// surface as the underlying pgx error so the service can detect duplicates.
func (q *Queries) Create(ctx context.Context, p CreateParams) (domain.User, error) {
	row := q.db.QueryRow(ctx, createSQL,
		p.ID, p.Username, p.DisplayName, p.Email, p.PasswordHash,
	)
	u, err := scanUser(row)
	if err != nil {
		return domain.User{}, fmt.Errorf("user: create: %w", err)
	}
	return u, nil
}

// GetByID returns the user (excluding soft-deleted). ErrNotFound on miss.
func (q *Queries) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	u, err := scanUser(q.db.QueryRow(ctx, getByIDSQL, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("user: get by id: %w", err)
	}
	return u, nil
}

// GetByIDIncludingDeleted returns the user even if soft-deleted (§4.6).
// Used for message-history attribution; handlers still strip sensitive
// fields at the DTO boundary.
func (q *Queries) GetByIDIncludingDeleted(ctx context.Context, id uuid.UUID) (domain.User, error) {
	u, err := scanUser(q.db.QueryRow(ctx, getByIDIncludingDeletedSQL, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("user: get by id including deleted: %w", err)
	}
	return u, nil
}

// GetByUsername returns the user. ErrNotFound on miss / soft-deleted.
// Username is citext, so case-insensitive comparison happens in postgres.
func (q *Queries) GetByUsername(ctx context.Context, username string) (domain.User, error) {
	u, err := scanUser(q.db.QueryRow(ctx, getByUsernameSQL, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("user: get by username: %w", err)
	}
	return u, nil
}

// GetByEmail returns the user. ErrNotFound on miss / soft-deleted.
// Email is citext (case-insensitive comparison handled by postgres).
func (q *Queries) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	u, err := scanUser(q.db.QueryRow(ctx, getByEmailSQL, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("user: get by email: %w", err)
	}
	return u, nil
}

// Update patches the writable profile fields. Pass nil for fields that
// should stay unchanged (COALESCE pattern). ErrNotFound when the row is
// missing or soft-deleted.
func (q *Queries) Update(ctx context.Context, p UpdateParams) (domain.User, error) {
	u, err := scanUser(q.db.QueryRow(ctx, updateSQL, p.ID, p.DisplayName, p.AvatarURL, p.ColorScheme))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("user: update: %w", err)
	}
	return u, nil
}

// UpdatePassword sets a new password hash. Caller is responsible for
// hashing via internal/argon2id.
func (q *Queries) UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	tag, err := q.db.Exec(ctx, updatePasswordSQL, id, passwordHash)
	if err != nil {
		return fmt.Errorf("user: update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRole sets a new role ("user" or "admin"). Used by admin handlers
// in milestone 12.5.
func (q *Queries) UpdateRole(ctx context.Context, id uuid.UUID, role string) error {
	tag, err := q.db.Exec(ctx, updateRoleSQL, id, role)
	if err != nil {
		return fmt.Errorf("user: update role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDelete sets deleted_at = now(). The user row stays in place; their
// content (messages, friendships) is preserved per §4.6.
func (q *Queries) SoftDelete(ctx context.Context, id uuid.UUID) error {
	tag, err := q.db.Exec(ctx, softDeleteSQL, id)
	if err != nil {
		return fmt.Errorf("user: soft delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByPrefix returns up to limit users whose username or display_name
// starts with q (case-insensitive). q="" returns all non-deleted users
// in (created_at DESC, id DESC) order. Pass cursor=nil for the first page.
//
// Always over-fetches limit+1 so the service layer can use pagination.Page
// to compute next_cursor + has_more.
func (q *Queries) ListByPrefix(ctx context.Context, prefix string, cursor *pagination.Cursor, limit int) ([]domain.User, error) {
	if limit <= 0 {
		limit = pagination.DefaultLimit
	}
	overFetch := limit + 1

	var ts *time.Time
	var id *uuid.UUID
	if cursor != nil {
		ts = &cursor.Timestamp
		id = &cursor.ID
	}

	rows, err := q.db.Query(ctx, listByPrefixSQL, prefix, ts, id, overFetch)
	if err != nil {
		return nil, fmt.Errorf("user: list by prefix: %w", err)
	}
	defer rows.Close()

	users := make([]domain.User, 0, overFetch)
	for rows.Next() {
		u, scanErr := scanUser(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("user: list by prefix scan: %w", scanErr)
		}
		users = append(users, u)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("user: list by prefix rows: %w", rowsErr)
	}
	return users, nil
}

// ListByIDs fetches every user whose ID appears in ids. Results are NOT
// guaranteed to be in input order — the caller maps by ID if order matters.
// Soft-deleted users ARE included so message-history rendering stays
// consistent with §4.6 (handlers strip via DTO converter).
func (q *Queries) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.db.Query(ctx, listByIDsSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("user: list by ids: %w", err)
	}
	defer rows.Close()

	users := make([]domain.User, 0, len(ids))
	for rows.Next() {
		u, scanErr := scanUser(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("user: list by ids scan: %w", scanErr)
		}
		users = append(users, u)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("user: list by ids rows: %w", rowsErr)
	}
	return users, nil
}
