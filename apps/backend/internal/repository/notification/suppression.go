// Package notification holds DB access for push-suppression lookups —
// the §10.2 gates on presence_states.intent (sticky DND) and
// conversation_members.muted_until (per-member mute). The service
// layer (internal/service/notification) consumes a SuppressionChecker
// interface that this package satisfies; this keeps SQL out of the
// service package per the §4.1 layered architecture rule.
package notification

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// Queries is the per-aggregate suppression repository.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// pushSuppressedSQL gathers both gates in one round-trip:
//
//   - dnd: true when presence_states.intent = 'dnd' for the user.
//   - muted: true when convID is non-NULL and the (convID, userID)
//     membership row's muted_until is in the future.
//
// COALESCE keeps the result deterministic when no presence row exists
// yet (treat as "not DND"). The convID arg is text so the same
// statement handles "no conv scope" via NULL — pgx encodes a nil
// *uuid.UUID as NULL.
const pushSuppressedSQL = `
SELECT
  COALESCE((SELECT intent = 'dnd' FROM presence_states WHERE user_id = $1), false) AS dnd,
  CASE WHEN $2::uuid IS NULL THEN false
       ELSE COALESCE((
           SELECT muted_until > now()
           FROM conversation_members
           WHERE user_id = $1 AND conversation_id = $2::uuid
       ), false)
  END AS muted`

// PushSuppressed reports whether a push to userID should be dropped.
// Returns true when the recipient's sticky DND intent is set OR
// (when convID is non-nil) the per-conversation mute is active.
//
// pgx.ErrNoRows can't surface from this query because every branch
// uses COALESCE to a literal default — but we fall through to
// (false, nil) if it does, rather than panic.
func (q *Queries) PushSuppressed(ctx context.Context, userID uuid.UUID, convID *uuid.UUID) (bool, error) {
	var dnd, muted bool
	row := q.db.QueryRow(ctx, pushSuppressedSQL, userID, convID)
	if err := row.Scan(&dnd, &muted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("notification: push-suppressed scan: %w", err)
	}
	return dnd || muted, nil
}
