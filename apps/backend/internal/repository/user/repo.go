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
RETURNING id, username, display_name, email, password_hash, avatar_url, bio, status_emoji, color_scheme, role, onboarded_at, created_at, updated_at, deleted_at`

const getByIDSQL = `-- name: GetByID :one
SELECT id, username, display_name, email, password_hash, avatar_url, bio, status_emoji, color_scheme, role, onboarded_at, created_at, updated_at, deleted_at
FROM users
WHERE id = $1 AND deleted_at IS NULL`

const getByIDIncludingDeletedSQL = `-- name: GetByIDIncludingDeleted :one
SELECT id, username, display_name, email, password_hash, avatar_url, bio, status_emoji, color_scheme, role, onboarded_at, created_at, updated_at, deleted_at
FROM users
WHERE id = $1`

const getByUsernameSQL = `-- name: GetByUsername :one
SELECT id, username, display_name, email, password_hash, avatar_url, bio, status_emoji, color_scheme, role, onboarded_at, created_at, updated_at, deleted_at
FROM users
WHERE username = $1 AND deleted_at IS NULL`

const getByEmailSQL = `-- name: GetByEmail :one
SELECT id, username, display_name, email, password_hash, avatar_url, bio, status_emoji, color_scheme, role, onboarded_at, created_at, updated_at, deleted_at
FROM users
WHERE email = $1 AND deleted_at IS NULL`

const updateSQL = `-- name: Update :one
UPDATE users
SET display_name = COALESCE($2, display_name),
    avatar_url   = COALESCE($3, avatar_url),
    color_scheme = COALESCE($4, color_scheme),
    bio          = COALESCE($5, bio),
    status_emoji = COALESCE($6, status_emoji)
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, username, display_name, email, password_hash, avatar_url, bio, status_emoji, color_scheme, role, onboarded_at, created_at, updated_at, deleted_at`

const updatePasswordSQL = `-- name: UpdatePassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1 AND deleted_at IS NULL`

const updateRoleSQL = `-- name: UpdateRole :exec
UPDATE users SET role = $2 WHERE id = $1 AND deleted_at IS NULL`

const softDeleteSQL = `-- name: SoftDelete :exec
UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`

const markOnboardedSQL = `-- name: MarkOnboarded :exec
UPDATE users
SET onboarded_at = COALESCE(onboarded_at, now())
WHERE id = $1 AND deleted_at IS NULL`

// clearAvatarSQL is the dedicated "remove the user's profile photo"
// path. The general Update query treats nil as "leave unchanged" via
// COALESCE so it can't represent "blow this column to NULL" — hence
// the separate query. Returns the previous avatar_url so the service
// can best-effort delete the underlying S3 object without an extra
// round-trip.
const clearAvatarSQL = `-- name: ClearAvatar :one
UPDATE users
SET avatar_url = NULL
WHERE id = $1 AND deleted_at IS NULL
RETURNING avatar_url`

// countByPrefixSQL mirrors listByPrefixSQL's WHERE clause minus the
// keyset cursor filter so it returns the absolute population
// matching the search — what the UI uses for "showing N of M"
// hints. The cursor is intentionally absent because the cursor
// filters mid-page, not the population.
const countByPrefixSQL = `-- name: CountByPrefix :one
SELECT COUNT(*)
FROM users
WHERE deleted_at IS NULL
  AND (
    $1::text = ''
    OR username ILIKE '%' || $1::text || '%' ESCAPE '\'
    OR display_name ILIKE '%' || $1::text || '%' ESCAPE '\'
  )
  AND (
    $2::uuid IS NULL
    OR NOT EXISTS (
      SELECT 1 FROM friendships f
      WHERE f.status = 'blocked'
        AND ((f.requester_id = $2::uuid AND f.addressee_id = users.id)
          OR (f.requester_id = users.id AND f.addressee_id = $2::uuid))
    )
  )`

// listByPrefixSQL hides users on either side of a 'blocked' friendship
// row from the caller — both directions, so blocking is symmetric in
// search visibility (Discord/Instagram convention). When $5 is NULL
// the NOT EXISTS clause short-circuits true and no filtering happens
// — that's the admin / system path that wants the full catalog.
//
// `ILIKE '%' || q || '%'` does substring matching, not just prefix.
// Typing "den" finds "caden" — what users expect when searching
// for a friend by partial name. Matches the contained-anywhere
// behavior the conversation search uses for group member names so
// the two sections feel consistent.
//
// Two rank columns drive the ordering, ahead of recency:
//   - rel_tier: friends (0) → pending (1) → strangers (2). The LEFT
//     JOIN attaches at most one friendships row per user (the
//     pair-unique index guarantees that). When $5 is NULL (admin
//     path) the JOIN matches nothing and every user lands in tier 2.
//   - match_rank: how closely the row matches the query — exact
//     username (0) → username starts-with (1) → exact display_name
//     (2) → display_name starts-with (3) → substring-only (4). So a
//     search for "user4" surfaces the exact "user4" above "user499".
//     $1 is the LIKE-escaped query (for the prefix checks); $7 is
//     the raw query (for the exact-equality checks — usernames can
//     contain `_`, which $1 would have escaped).
//
// Within a (rel_tier, match_rank) bucket the tiebreak is
// created_at ASC, id ASC — oldest accounts first. For sequentially
// named accounts that reads as "closest first" ("user4" → "user40"
// → … → "user499" rather than the newest "user4*" jumping ahead);
// for everything else "established account first" is a sane default.
//
// Keyset pagination with (rel_tier ASC, match_rank ASC, created_at
// ASC, id ASC): "the row after the cursor" is the OR-block below.
// $6 is the cursor's rel_tier (NULL on the first page → no keyset
// filter); $8 is the cursor's match_rank.
const listByPrefixSQL = `-- name: ListByPrefix :many
WITH ranked AS (
  SELECT u.id, u.username, u.display_name, u.email, u.password_hash, u.avatar_url,
         u.bio, u.status_emoji, u.color_scheme, u.role, u.onboarded_at,
         u.created_at, u.updated_at, u.deleted_at,
         CASE
           WHEN $5::uuid IS NULL THEN 2
           WHEN f.status = 'accepted' THEN 0
           WHEN f.status = 'pending' THEN 1
           ELSE 2
         END AS rel_tier,
         CASE
           WHEN $7::text = '' THEN 1
           WHEN lower(u.username) = lower($7::text) THEN 0
           WHEN lower(u.username) LIKE lower($1::text) || '%' ESCAPE '\' THEN 1
           WHEN lower(u.display_name) = lower($7::text) THEN 2
           WHEN lower(u.display_name) LIKE lower($1::text) || '%' ESCAPE '\' THEN 3
           ELSE 4
         END AS match_rank
  FROM users u
  LEFT JOIN friendships f
    ON (
         (f.requester_id = $5::uuid AND f.addressee_id = u.id)
         OR (f.requester_id = u.id AND f.addressee_id = $5::uuid)
       )
   AND f.status IN ('accepted', 'pending')
  WHERE u.deleted_at IS NULL
    AND (
      $1::text = ''
      OR u.username ILIKE '%' || $1::text || '%' ESCAPE '\'
      OR u.display_name ILIKE '%' || $1::text || '%' ESCAPE '\'
    )
    AND (
      $5::uuid IS NULL
      OR NOT EXISTS (
        SELECT 1 FROM friendships fb
        WHERE fb.status = 'blocked'
          AND ((fb.requester_id = $5::uuid AND fb.addressee_id = u.id)
            OR (fb.requester_id = u.id AND fb.addressee_id = $5::uuid))
      )
    )
)
SELECT id, username, display_name, email, password_hash, avatar_url, bio,
       status_emoji, color_scheme, role, onboarded_at,
       created_at, updated_at, deleted_at, rel_tier, match_rank
FROM ranked
WHERE $6::int IS NULL
   OR rel_tier > $6::int
   OR (rel_tier = $6::int AND match_rank > $8::int)
   OR (rel_tier = $6::int AND match_rank = $8::int AND created_at > $2::timestamptz)
   OR (rel_tier = $6::int AND match_rank = $8::int AND created_at = $2::timestamptz AND id > $3::uuid)
ORDER BY rel_tier ASC, match_rank ASC, created_at ASC, id ASC
LIMIT $4`

// escapeLikePrefix backslash-escapes the LIKE metacharacters \, %, and _
// so user-supplied search input like "100%" matches a literal "100%"
// instead of being treated as wildcards. The SQL uses an explicit
// `ESCAPE '\'` clause so behavior doesn't depend on PG's default
// (which has flipped between versions).
//
// One-pass byte walk is enough because all three target chars are ASCII;
// no need for utf8 decoding even if the rest of the input is multi-byte.
func escapeLikePrefix(s string) string {
	if !needsLikeEscape(s) {
		return s
	}
	b := make([]byte, 0, len(s)+4)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '%' || c == '_' {
			b = append(b, '\\')
		}
		b = append(b, c)
	}
	return string(b)
}

func needsLikeEscape(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '%', '_':
			return true
		}
	}
	return false
}

const listByIDsSQL = `-- name: ListByIDs :many
SELECT id, username, display_name, email, password_hash, avatar_url, bio, status_emoji, color_scheme, role, onboarded_at, created_at, updated_at, deleted_at
FROM users
WHERE id = ANY($1::uuid[])`

const matchByEmailHashesSQL = `-- name: MatchByEmailHashes :many
SELECT id, username, display_name, email, password_hash, avatar_url, bio, status_emoji, color_scheme, role, onboarded_at, created_at, updated_at, deleted_at
FROM users
WHERE deleted_at IS NULL
  AND email_hash = ANY(
      SELECT decode(h, 'hex') FROM unnest($1::text[]) AS h
  )`

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
		&u.Bio,
		&u.StatusEmoji,
		&u.ColorScheme,
		&u.Role,
		&u.OnboardedAt,
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
	u, err := scanUser(q.db.QueryRow(ctx, updateSQL,
		p.ID, p.DisplayName, p.AvatarURL, p.ColorScheme, p.Bio, p.StatusEmoji,
	))
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

// MarkOnboarded stamps onboarded_at = now() on the row, idempotently —
// a second call leaves the existing timestamp in place via COALESCE so
// the carousel can re-trigger the endpoint without bumping the value.
// Used by the mobile post-login onboarding carousel (WAKEUPEXPO §3.0)
// when the user finishes the slides.
func (q *Queries) MarkOnboarded(ctx context.Context, id uuid.UUID) error {
	tag, err := q.db.Exec(ctx, markOnboardedSQL, id)
	if err != nil {
		return fmt.Errorf("user: mark onboarded: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearAvatar sets the user's avatar_url to NULL and returns the
// previous value so callers can purge the underlying S3 object.
// Returns ("", nil) when the column was already empty — that's a
// no-op success, not an error. ErrNotFound when the user row is
// missing or soft-deleted.
func (q *Queries) ClearAvatar(ctx context.Context, id uuid.UUID) (string, error) {
	var prev *string
	err := q.db.QueryRow(ctx, clearAvatarSQL, id).Scan(&prev)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("user: clear avatar: %w", err)
	}
	if prev == nil {
		return "", nil
	}
	return *prev, nil
}

// SearchHit pairs a user with the caller's relationship tier
// (0 = accepted friend, 1 = pending in either direction, 2 = no
// relationship / admin path) and the row's match rank against the
// query (0 = exact username … 4 = substring-only; see
// listByPrefixSQL). The service uses both to build the keyset
// cursor; the client gets friends-first, closest-match-first order
// without any client-side sort.
type SearchHit struct {
	User      domain.User
	Tier      int
	MatchRank int
}

// ListByPrefix returns up to limit search hits whose username or
// display_name contains the query (case-insensitive substring).
// Results are ordered "friends → pending → everyone else", then by
// match rank (exact username → username prefix → display-name
// matches → substring-only), then by (created_at ASC, id ASC)
// within each (tier, rank) bucket — so an exact "user4" leads,
// followed by "user40", "user41", … rather than "user499". Pass
// cursor=nil for the first page; subsequent pages supply a cursor
// whose Tier and MatchRank fields come from the previous page's
// last row.
//
// callerID, when non-nil, hides users on either side of a 'blocked'
// friendship row with that caller — symmetric block visibility so
// neither party finds the other in search. Pass nil for callers
// that should bypass the filter (admin user lookup, internal
// system paths); the SQL CASE collapses all rows to tier 2 in
// that branch, which mirrors the legacy ordering.
//
// Always over-fetches limit+1 so the service layer can use
// pagination.Page to compute next_cursor + has_more.
func (q *Queries) ListByPrefix(ctx context.Context, prefix string, callerID *uuid.UUID, cursor *pagination.Cursor, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = pagination.DefaultLimit
	}
	overFetch := limit + 1

	var ts *time.Time
	var id *uuid.UUID
	var tier *int
	var matchRank *int
	if cursor != nil {
		ts = &cursor.Timestamp
		id = &cursor.ID
		tier = cursor.Tier
		matchRank = cursor.MatchRank
	}

	// $1 = LIKE-escaped query (prefix/substring checks); $7 = raw query
	// (exact-equality checks — usernames can contain `_`, which the
	// escape would have turned into `\_`). The SQL's `ESCAPE '\'`
	// clauses honor the escapes on $1.
	rows, err := q.db.Query(
		ctx, listByPrefixSQL, escapeLikePrefix(prefix), ts, id, overFetch, callerID, tier, prefix, matchRank,
	)
	if err != nil {
		return nil, fmt.Errorf("user: list by prefix: %w", err)
	}
	defer rows.Close()

	hits := make([]SearchHit, 0, overFetch)
	for rows.Next() {
		var u domain.User
		var relTier, matchRankCol int
		if scanErr := rows.Scan(
			&u.ID,
			&u.Username,
			&u.DisplayName,
			&u.Email,
			&u.PasswordHash,
			&u.AvatarURL,
			&u.Bio,
			&u.StatusEmoji,
			&u.ColorScheme,
			&u.Role,
			&u.OnboardedAt,
			&u.CreatedAt,
			&u.UpdatedAt,
			&u.DeletedAt,
			&relTier,
			&matchRankCol,
		); scanErr != nil {
			return nil, fmt.Errorf("user: list by prefix scan: %w", scanErr)
		}
		hits = append(hits, SearchHit{User: u, Tier: relTier, MatchRank: matchRankCol})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("user: list by prefix rows: %w", rowsErr)
	}
	return hits, nil
}

// CountByPrefix returns the absolute count of users matching the
// substring search — same WHERE clause as ListByPrefix but no
// keyset cursor filter (cursor pages the slice, not the
// population). Used for the "X of N" hint above paginated lists.
func (q *Queries) CountByPrefix(ctx context.Context, prefix string, callerID *uuid.UUID) (int, error) {
	var n int
	if err := q.db.QueryRow(ctx, countByPrefixSQL, escapeLikePrefix(prefix), callerID).Scan(&n); err != nil {
		return 0, fmt.Errorf("user: count by prefix: %w", err)
	}
	return n, nil
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

// MatchByEmailHashes returns active users whose email's SHA-256 (lower-
// cased, hex-encoded by the client) appears in `hexHashes`. Each entry
// must be exactly 64 lowercase hex chars — the service validates the
// shape so a malformed input fails the whole batch with a typed error
// rather than reaching `decode` and panicking. Returned users are not
// in any guaranteed order; the caller maps by ID / hash if order matters.
//
// Soft-deleted users are excluded by the partial index condition; an
// account that's been deleted shouldn't surface in contact-sync results.
func (q *Queries) MatchByEmailHashes(ctx context.Context, hexHashes []string) ([]domain.User, error) {
	if len(hexHashes) == 0 {
		return nil, nil
	}
	rows, err := q.db.Query(ctx, matchByEmailHashesSQL, hexHashes)
	if err != nil {
		return nil, fmt.Errorf("user: match by email hashes: %w", err)
	}
	defer rows.Close()

	users := make([]domain.User, 0, len(hexHashes))
	for rows.Next() {
		u, scanErr := scanUser(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("user: match by email hashes scan: %w", scanErr)
		}
		users = append(users, u)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("user: match by email hashes rows: %w", rowsErr)
	}
	return users, nil
}
