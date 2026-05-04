package notification

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// PushSuppression implements SuppressionChecker by reading
// presence_states.intent and conversation_members.muted_until in a
// single round-trip. Construct via NewPushSuppression(pool); the
// production wiring lives in cmd/server/main.go.
type PushSuppression struct {
	db storage.DBTX
}

// NewPushSuppression builds the adapter.
func NewPushSuppression(db storage.DBTX) (*PushSuppression, error) {
	if db == nil {
		return nil, errors.New("notification: NewPushSuppression requires a DBTX")
	}
	return &PushSuppression{db: db}, nil
}

// pushSuppressedSQL gathers both gates in one round-trip:
//
//   - dnd: true when presence_states.intent = 'dnd' for the user.
//   - muted: true when convID is non-NULL and the (convID, userID)
//     membership row's muted_until is in the future.
//
// COALESCE keeps the result deterministic when no presence row exists
// yet (treat as "not DND"). The convID arg is text so the same statement
// handles "no conv scope" via NULL — pgx encodes a nil *uuid.UUID as NULL.
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

// PushSuppressed implements SuppressionChecker. Returns true when the
// recipient's sticky DND intent OR per-conversation mute should drop
// this push. Errors propagate so the caller can decide whether to log
// + drop or surface; per §11 the existing pattern is to log + drop.
func (s *PushSuppression) PushSuppressed(ctx context.Context, userID uuid.UUID, convID *uuid.UUID) (bool, error) {
	var dnd, muted bool
	row := s.db.QueryRow(ctx, pushSuppressedSQL, userID, convID)
	if err := row.Scan(&dnd, &muted); err != nil {
		// pgx.ErrNoRows can't happen with the COALESCE form above, but
		// fall through defensively rather than panic.
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("notification: push-suppressed scan: %w", err)
	}
	return dnd || muted, nil
}

// NeverSuppress is a checker that never suppresses, regardless of
// input. Equivalent to passing nil Suppression in Config; exported so
// tests in other packages can opt out of suppression explicitly.
var NeverSuppress SuppressionChecker = neverSuppress{}

type neverSuppress struct{}

func (neverSuppress) PushSuppressed(context.Context, uuid.UUID, *uuid.UUID) (bool, error) {
	return false, nil
}
