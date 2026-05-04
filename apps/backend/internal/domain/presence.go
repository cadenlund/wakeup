package domain

import (
	"time"

	"github.com/google/uuid"
)

// PresenceStatus is the §7.2 / §16 set of values stored in the
// presence_states table. The schema CHECK enforces the same set; the
// constants are repeated here so callers can compare at the domain
// layer without depending on a string literal.
type PresenceStatus string

// Presence status values mirror the migration 0007 CHECK constraint.
const (
	PresenceOnline   PresenceStatus = "online"
	PresenceAway     PresenceStatus = "away"
	PresenceOffline  PresenceStatus = "offline"
	PresenceSleeping PresenceStatus = "sleeping"
	PresenceDND      PresenceStatus = "dnd"
)

// IsValid reports whether s is one of the known statuses.
func (s PresenceStatus) IsValid() bool {
	switch s {
	case PresenceOnline, PresenceAway, PresenceOffline, PresenceSleeping, PresenceDND:
		return true
	}
	return false
}

// IsValidIntent reports whether s is a valid value for `intent` — the
// user-set sticky override. Same as IsValid minus offline (you can't
// manually mark yourself offline; logout does that).
func (s PresenceStatus) IsValidIntent() bool {
	switch s {
	case PresenceOnline, PresenceAway, PresenceSleeping, PresenceDND:
		return true
	}
	return false
}

// Ptr returns a pointer to s. Convenience helper for the SetStatus API
// (which takes *PresenceStatus to model "set or clear") so callers can
// pass a literal: `svc.SetStatus(ctx, uid, domain.PresenceDND.Ptr())`.
func (s PresenceStatus) Ptr() *PresenceStatus { return &s }

// PresenceState mirrors a row in presence_states (migration 0007).
//
// last_active_at is the last time the user did anything — heartbeat,
// REST request, or status change. The §9.2 decay sweeper compares
// this against `now()` to demote online → away → offline.
//
// last_heartbeat_at is the WS-specific last-ping timestamp. Tracked
// separately so a user who's actively REST-ing but doesn't have a
// live WS connection still counts as "online".
type PresenceState struct {
	UserID uuid.UUID
	Status PresenceStatus
	// Intent is the user-set sticky override. Non-nil → WS hub leaves
	// Status alone (DND survives app backgrounding). Nil → status moves
	// with connection state and decay.
	Intent          *PresenceStatus
	LastActiveAt    time.Time
	LastHeartbeatAt time.Time
	UpdatedAt       time.Time
}
