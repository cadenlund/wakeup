package domain

import (
	"time"

	"github.com/google/uuid"
)

// NotificationPreference mirrors a row in `notification_preferences`
// (migration 0012). One row per user; all booleans default to true and
// the theme columns default to 'system' (the row is auto-created on
// first read by the repository).
//
// The table's name predates the theme additions — it now holds both
// notification toggles AND the mobile §4.5 theme pick, on the
// reasoning that "user prefs" naturally cluster onto one row. Renaming
// the table to `user_preferences` is a larger refactor we defer.
//
// Notification categories map to trigger sites (§16 milestone 11.5):
//   - DirectMessages: MessageService.Send to a direct conversation
//   - GroupMessages : MessageService.Send to a group
//   - FriendRequests: FriendService.SendRequest
//   - Calls         : CallService.InitiateCall (room.started)
//
// Theme fields:
//   - ThemeScheme: one of "system", "sunrise", "daylight", "noon",
//     "golden", "meadow", "dusk", "twilight", "aurora", "midnight",
//     "rem". "system" lets the client resolve a default (daylight in
//     light, midnight in dark).
//   - ThemeModePreference: "system" (follow OS Appearance), "light",
//     or "dark". The client honors this independently of ThemeScheme
//     so a user can pin "always dark" while still picking color
//     personality.
type NotificationPreference struct {
	UserID              uuid.UUID
	DirectMessages      bool
	GroupMessages       bool
	FriendRequests      bool
	Calls               bool
	ThemeScheme         string
	ThemeModePreference string
	UpdatedAt           time.Time
}
