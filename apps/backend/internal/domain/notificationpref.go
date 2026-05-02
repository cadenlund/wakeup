package domain

import (
	"time"

	"github.com/google/uuid"
)

// NotificationPreference mirrors a row in `notification_preferences`
// (migration 0012). One row per user, all booleans default to true (the
// row is auto-created on first read by the repository).
//
// Categories map to the trigger sites (§16 milestone 11.5):
//   - DirectMessages: MessageService.Send to a direct conversation
//   - GroupMessages : MessageService.Send to a group
//   - FriendRequests: FriendService.SendRequest
//   - Calls         : CallService.InitiateCall (room.started)
type NotificationPreference struct {
	UserID         uuid.UUID
	DirectMessages bool
	GroupMessages  bool
	FriendRequests bool
	Calls          bool
	UpdatedAt      time.Time
}
