// Package conversation is the data-access layer for the conversations +
// conversation_members tables (migration 0004). The state-machine
// values (type / role) live in `internal/domain/conversation.go`.
//
// Group cap: §4.6 limits group conversations to 25 members. The repo
// CountMembers query exposes the count so the service layer can refuse
// adds before they happen — the migration doesn't carry a CHECK
// because counting members at insert time isn't expressible as a
// row-level CHECK.
package conversation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// MaxGroupMembers is the §4.6 cap on group conversations. The service
// layer uses this for pre-write validation; the repo also accepts it
// as a guard inside CreateGroup so a misuse can't sneak past.
const MaxGroupMembers = 25

// ErrNotFound is the sentinel returned when a conversation or member
// row doesn't exist.
var ErrNotFound = errors.New("conversation: not found")

// ErrGroupTooLarge is returned when a group create / add would push
// the member count past MaxGroupMembers.
var ErrGroupTooLarge = fmt.Errorf("conversation: groups are capped at %d members", MaxGroupMembers)

// Queries is the per-aggregate repository.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx so the service can
// compose CreateConversation + AddMember atomically (§4.2).
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const createConversationSQL = `-- name: CreateConversation :one
INSERT INTO conversations (id, type, name, avatar_url, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, type, name, avatar_url, created_by, created_at, updated_at, last_message_at`

const getConversationSQL = `-- name: GetConversation :one
SELECT id, type, name, avatar_url, created_by, created_at, updated_at, last_message_at
FROM conversations
WHERE id = $1`

const updateConversationSQL = `-- name: UpdateConversation :one
UPDATE conversations
SET name       = COALESCE($2, name),
    avatar_url = COALESCE($3, avatar_url)
WHERE id = $1
RETURNING id, type, name, avatar_url, created_by, created_at, updated_at, last_message_at`

const touchLastMessageAtSQL = `-- name: TouchLastMessageAt :exec
UPDATE conversations
SET last_message_at = $2
WHERE id = $1 AND last_message_at < $2`

const deleteConversationSQL = `-- name: DeleteConversation :exec
DELETE FROM conversations WHERE id = $1`

const listConversationsByUserSQL = `-- name: ListConversationsByUser :many
SELECT c.id, c.type, c.name, c.avatar_url, c.created_by,
       c.created_at, c.updated_at, c.last_message_at
FROM conversations c
JOIN conversation_members m ON m.conversation_id = c.id
WHERE m.user_id = $1
  AND ($2::timestamptz IS NULL OR ($2::timestamptz, $3::uuid) > (c.last_message_at, c.id))
ORDER BY c.last_message_at DESC, c.id DESC
LIMIT $4`

const getDirectByPairSQL = `-- name: GetDirectByPair :one
SELECT c.id, c.type, c.name, c.avatar_url, c.created_by,
       c.created_at, c.updated_at, c.last_message_at
FROM conversations c
JOIN conversation_members ma ON ma.conversation_id = c.id AND ma.user_id = $1
JOIN conversation_members mb ON mb.conversation_id = c.id AND mb.user_id = $2
WHERE c.type = 'direct' AND $1::uuid <> $2::uuid`

const addMemberSQL = `-- name: AddMember :one
INSERT INTO conversation_members (conversation_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING conversation_id, user_id, role, joined_at, last_read_message_id`

const lockConversationForMemberWriteSQL = `-- name: LockConversationForMemberWrite :one
SELECT id FROM conversations WHERE id = $1 FOR UPDATE`

const removeMemberSQL = `-- name: RemoveMember :exec
DELETE FROM conversation_members
WHERE conversation_id = $1 AND user_id = $2`

const getMemberSQL = `-- name: GetMember :one
SELECT conversation_id, user_id, role, joined_at, last_read_message_id
FROM conversation_members
WHERE conversation_id = $1 AND user_id = $2`

const listMembersSQL = `-- name: ListMembers :many
SELECT conversation_id, user_id, role, joined_at, last_read_message_id
FROM conversation_members
WHERE conversation_id = $1
ORDER BY joined_at ASC, user_id ASC`

const countMembersSQL = `-- name: CountMembers :one
SELECT count(*) FROM conversation_members WHERE conversation_id = $1`

const updateLastReadMessageSQL = `-- name: UpdateLastReadMessage :exec
UPDATE conversation_members
SET last_read_message_id = $3
WHERE conversation_id = $1 AND user_id = $2`

// CreateParams is the input to CreateConversation. Direct conversations
// pass nil Name + AvatarURL; groups pass at least Name. The repo
// doesn't enforce that — the service does.
type CreateParams struct {
	ID        uuid.UUID
	Type      domain.ConversationType
	Name      *string
	AvatarURL *string
	CreatedBy uuid.UUID
}

// scanConversation decodes one row into domain.Conversation.
func scanConversation(row pgx.Row) (domain.Conversation, error) {
	var c domain.Conversation
	err := row.Scan(
		&c.ID, &c.Type, &c.Name, &c.AvatarURL, &c.CreatedBy,
		&c.CreatedAt, &c.UpdatedAt, &c.LastMessageAt,
	)
	return c, err
}

// scanMember decodes one row into domain.ConversationMember.
func scanMember(row pgx.Row) (domain.ConversationMember, error) {
	var m domain.ConversationMember
	err := row.Scan(
		&m.ConversationID, &m.UserID, &m.Role, &m.JoinedAt, &m.LastReadMessageID,
	)
	return m, err
}

// CreateConversation inserts a new conversation row. Doesn't add any
// members — the service calls AddMember per participant inside the
// same transaction.
func (q *Queries) CreateConversation(ctx context.Context, p CreateParams) (domain.Conversation, error) {
	c, err := scanConversation(q.db.QueryRow(ctx, createConversationSQL,
		p.ID, string(p.Type), p.Name, p.AvatarURL, p.CreatedBy))
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("conversation: create: %w", err)
	}
	return c, nil
}

// GetConversation returns the conversation row. ErrNotFound on miss.
func (q *Queries) GetConversation(ctx context.Context, id uuid.UUID) (domain.Conversation, error) {
	c, err := scanConversation(q.db.QueryRow(ctx, getConversationSQL, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Conversation{}, ErrNotFound
	}
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("conversation: get: %w", err)
	}
	return c, nil
}

// UpdateParams is the input to UpdateConversation. Pointer fields use
// nil-means-unchanged semantics matching the repo's COALESCE pattern.
type UpdateParams struct {
	ID        uuid.UUID
	Name      *string
	AvatarURL *string
}

// UpdateConversation patches name / avatar_url. ErrNotFound when no row
// matches id. Caller is responsible for restricting to group conversations.
func (q *Queries) UpdateConversation(ctx context.Context, p UpdateParams) (domain.Conversation, error) {
	c, err := scanConversation(q.db.QueryRow(ctx, updateConversationSQL, p.ID, p.Name, p.AvatarURL))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Conversation{}, ErrNotFound
	}
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("conversation: update: %w", err)
	}
	return c, nil
}

// TouchLastMessageAt bumps last_message_at when ts is newer than the
// stored value. Idempotent on equal/older timestamps. Used by the
// message service after a successful Send.
func (q *Queries) TouchLastMessageAt(ctx context.Context, id uuid.UUID, ts time.Time) error {
	if _, err := q.db.Exec(ctx, touchLastMessageAtSQL, id, ts); err != nil {
		return fmt.Errorf("conversation: touch last_message_at: %w", err)
	}
	return nil
}

// DeleteConversation removes the row + all its members (FK cascade).
func (q *Queries) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	tag, err := q.db.Exec(ctx, deleteConversationSQL, id)
	if err != nil {
		return fmt.Errorf("conversation: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListConversationsByUser returns conversations the user is a member of,
// keyset-paginated on (last_message_at DESC, id DESC).
func (q *Queries) ListConversationsByUser(ctx context.Context, userID uuid.UUID, cursor *pagination.Cursor, limit int) ([]domain.Conversation, error) {
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

	rows, err := q.db.Query(ctx, listConversationsByUserSQL, userID, ts, id, overFetch)
	if err != nil {
		return nil, fmt.Errorf("conversation: list by user: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Conversation, 0, overFetch)
	for rows.Next() {
		c, scanErr := scanConversation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("conversation: list by user scan: %w", scanErr)
		}
		out = append(out, c)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("conversation: list by user rows: %w", rowsErr)
	}
	return out, nil
}

// GetDirectByPair returns the (at most one) direct conversation between
// users a and b. ErrNotFound when no such row exists. Used by the
// service to dedupe direct creates.
func (q *Queries) GetDirectByPair(ctx context.Context, a, b uuid.UUID) (domain.Conversation, error) {
	c, err := scanConversation(q.db.QueryRow(ctx, getDirectByPairSQL, a, b))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Conversation{}, ErrNotFound
	}
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("conversation: get direct: %w", err)
	}
	return c, nil
}

// AddMember inserts a single conversation_members row. The PK on
// (conversation_id, user_id) prevents duplicates; concurrent adds to
// the same conversation can race past the cap-25 invariant — use
// AddMemberWithCap to enforce that atomically.
func (q *Queries) AddMember(ctx context.Context, conversationID, userID uuid.UUID, role domain.MemberRole) (domain.ConversationMember, error) {
	m, err := scanMember(q.db.QueryRow(ctx, addMemberSQL, conversationID, userID, string(role)))
	if err != nil {
		return domain.ConversationMember{}, fmt.Errorf("conversation: add member: %w", err)
	}
	return m, nil
}

// AddMemberWithCap inserts a single conversation_members row, refusing
// when the conversation is already at `memberCap` members.
//
// Atomicity is multi-statement and requires its own transaction:
//
//  1. SELECT FROM conversations WHERE id=$1 FOR UPDATE — locks the row,
//     concurrent writers block here.
//  2. SELECT count(*) FROM conversation_members WHERE conversation_id=$1.
//     This second statement gets a fresh READ COMMITTED snapshot AFTER
//     the FOR UPDATE returns, so concurrent inserts that committed
//     before our lock acquired ARE visible (Postgres `FOR UPDATE` plus
//     follow-the-lock semantics — see PR #34 review for why a single-
//     statement CTE is NOT enough).
//  3. INSERT IF count < cap.
//
// We need a *pgxpool.Pool here because we BEGIN our own transaction;
// the storage.DBTX interface doesn't expose Begin. Returns
// ErrGroupTooLarge when the cap is hit; ErrNotFound when the
// conversation row doesn't exist; SQLSTATE 23505 unique-violation when
// the user is already a member.
func AddMemberWithCap(ctx context.Context, pool *pgxpool.Pool, conversationID, userID uuid.UUID, role domain.MemberRole, memberCap int) (domain.ConversationMember, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return domain.ConversationMember{}, fmt.Errorf("conversation: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Step 1: row-lock the conversation. Concurrent calls block here.
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, lockConversationForMemberWriteSQL, conversationID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ConversationMember{}, ErrNotFound
		}
		return domain.ConversationMember{}, fmt.Errorf("conversation: lock for member write: %w", err)
	}

	// Step 2: count after lock. Fresh snapshot in this new statement
	// reflects any inserts committed by writers that ran before us.
	var count int
	if err := tx.QueryRow(ctx, countMembersSQL, conversationID).Scan(&count); err != nil {
		return domain.ConversationMember{}, fmt.Errorf("conversation: count members: %w", err)
	}
	if count >= memberCap {
		return domain.ConversationMember{}, ErrGroupTooLarge
	}

	// Step 3: insert.
	m, err := scanMember(tx.QueryRow(ctx, addMemberSQL, conversationID, userID, string(role)))
	if err != nil {
		return domain.ConversationMember{}, fmt.Errorf("conversation: add member with cap: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ConversationMember{}, fmt.Errorf("conversation: commit add member with cap: %w", err)
	}
	return m, nil
}

// RemoveMember deletes a conversation_members row. ErrNotFound when no
// such row exists.
func (q *Queries) RemoveMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	tag, err := q.db.Exec(ctx, removeMemberSQL, conversationID, userID)
	if err != nil {
		return fmt.Errorf("conversation: remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMember returns a single membership row. ErrNotFound when no such
// row.
func (q *Queries) GetMember(ctx context.Context, conversationID, userID uuid.UUID) (domain.ConversationMember, error) {
	m, err := scanMember(q.db.QueryRow(ctx, getMemberSQL, conversationID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ConversationMember{}, ErrNotFound
	}
	if err != nil {
		return domain.ConversationMember{}, fmt.Errorf("conversation: get member: %w", err)
	}
	return m, nil
}

// ListMembers returns every member in the conversation ordered by
// joined_at ASC.
func (q *Queries) ListMembers(ctx context.Context, conversationID uuid.UUID) ([]domain.ConversationMember, error) {
	rows, err := q.db.Query(ctx, listMembersSQL, conversationID)
	if err != nil {
		return nil, fmt.Errorf("conversation: list members: %w", err)
	}
	defer rows.Close()

	var out []domain.ConversationMember
	for rows.Next() {
		m, scanErr := scanMember(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("conversation: list members scan: %w", scanErr)
		}
		out = append(out, m)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("conversation: list members rows: %w", rowsErr)
	}
	return out, nil
}

// CountMembers returns the number of members in a conversation. Service
// layer calls this before AddMember to enforce the cap-25 rule (§4.6).
func (q *Queries) CountMembers(ctx context.Context, conversationID uuid.UUID) (int, error) {
	var n int
	if err := q.db.QueryRow(ctx, countMembersSQL, conversationID).Scan(&n); err != nil {
		return 0, fmt.Errorf("conversation: count members: %w", err)
	}
	return n, nil
}

// UpdateLastReadMessage stamps the user's read pointer. Idempotent —
// no-op when the row doesn't exist (caller pre-validates membership).
func (q *Queries) UpdateLastReadMessage(ctx context.Context, conversationID, userID, messageID uuid.UUID) error {
	if _, err := q.db.Exec(ctx, updateLastReadMessageSQL, conversationID, userID, messageID); err != nil {
		return fmt.Errorf("conversation: update last read: %w", err)
	}
	return nil
}
