package domain

import (
	"time"

	"github.com/google/uuid"
)

// AuditLog mirrors a row in audit_log (migration 0010). Every admin or
// impersonation action writes one. Per §8.7, ActorID is the real admin
// user even during impersonation; the impersonated user surfaces in
// Metadata as `impersonating_user_id`.
//
// ActorID, TargetType, TargetID, and Metadata are nullable in the
// schema — a `system.startup` row, for example, has no actor.
type AuditLog struct {
	ID         uuid.UUID
	ActorID    *uuid.UUID
	Action     string
	TargetType *string
	TargetID   *uuid.UUID
	Metadata   map[string]any
	CreatedAt  time.Time
}
