package domain

import (
	"time"

	"github.com/google/uuid"
)

// FriendshipStatus is the §5.1 friendship state. Stored as a text column
// in the `friendships` table with a CHECK constraint matching these
// values exactly — keep wire shape lowercase per §4.6.
type FriendshipStatus string

// Locked friendship status values. Match the `status` CHECK constraint
// in migration 0003.
const (
	FriendshipPending  FriendshipStatus = "pending"
	FriendshipAccepted FriendshipStatus = "accepted"
	FriendshipBlocked  FriendshipStatus = "blocked"
)

// Friendship mirrors a row in the `friendships` table (migration 0003).
//
// Direction matters at the SQL level (requester vs addressee), but the
// service layer treats the pair as undirected for most reads — see the
// pair-unique index in the migration. The Status field carries the
// state-machine value.
//
// AcceptedAt is non-nil only after a 'pending' row transitions to
// 'accepted'. Soft-deleted users (§4.6) are NOT removed from this
// table — handlers strip via the user DTO converter.
type Friendship struct {
	ID          uuid.UUID
	RequesterID uuid.UUID
	AddresseeID uuid.UUID
	Status      FriendshipStatus
	CreatedAt   time.Time
	AcceptedAt  *time.Time
}

// OtherID returns the user_id on the row that ISN'T `self`. Used by the
// service to render "who is the friend" in list views without making
// the caller branch on direction.
func (f Friendship) OtherID(self uuid.UUID) uuid.UUID {
	if f.RequesterID == self {
		return f.AddresseeID
	}
	return f.RequesterID
}

// IsAccepted reports whether the friendship has been accepted. Callers
// often gate on this before letting two users chat.
func (f Friendship) IsAccepted() bool { return f.Status == FriendshipAccepted }

// IsBlocked reports whether the relationship is in the blocked state
// (in either direction).
func (f Friendship) IsBlocked() bool { return f.Status == FriendshipBlocked }
