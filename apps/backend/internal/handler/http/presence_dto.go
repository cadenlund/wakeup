package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// PresenceResponse is the wire shape for one user's presence row,
// matching the §7.2 presence.update payload (so the WS event and the
// REST response carry the same data).
type PresenceResponse struct {
	UserID       uuid.UUID `json:"user_id"        example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Status       string    `json:"status"         example:"online"`
	LastActiveAt time.Time `json:"last_active_at" example:"2026-05-02T10:42:55.412Z"`
}

// PresenceListResponse is the body of GET /v1/presence/friends.
type PresenceListResponse struct {
	Data []PresenceResponse `json:"data"`
}

// SetPresenceStatusRequest is the body of POST /v1/presence/status.
// Manual override for sticky presence intent. Allowed values:
//   - "online" / "away" / "sleeping" / "dnd": set as sticky intent;
//     status survives WS disconnect, the decay sweeper, and app
//     backgrounding until cleared.
//   - null: clear an existing sticky intent. Effective status falls
//     back to "online" and the WS hub / decay sweeper take back over.
//
// "offline" is intentionally not user-settable — that's what logout is.
type SetPresenceStatusRequest struct {
	Status *string `json:"status" validate:"omitempty,oneof=online away sleeping dnd" example:"dnd"`
}

// WidgetFriendRow is one row in GET /v1/widget/friends. The endpoint
// is designed for the §6.1 widget that polls every ~15min, so it
// embeds the user profile + presence in one shape — the widget can
// render the whole list without a follow-up /v1/users call.
type WidgetFriendRow struct {
	User     UserResponse     `json:"user"`
	Presence PresenceResponse `json:"presence"`
}

// WidgetFriendsResponse is the body of GET /v1/widget/friends.
type WidgetFriendsResponse struct {
	Data []WidgetFriendRow `json:"data"`
}

// toPresenceResponse converts a domain.PresenceState into the wire shape.
func toPresenceResponse(p domain.PresenceState) PresenceResponse {
	return PresenceResponse{
		UserID:       p.UserID,
		Status:       string(p.Status),
		LastActiveAt: p.LastActiveAt,
	}
}

// toPresenceList converts a slice.
func toPresenceList(rows []domain.PresenceState) []PresenceResponse {
	out := make([]PresenceResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPresenceResponse(r))
	}
	return out
}
