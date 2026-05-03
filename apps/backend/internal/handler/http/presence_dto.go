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
// Schema constraints mirror the §6.1 manual-override docs: only
// `online` and `sleeping` are user-settable. The service's broader
// allow-list (which also covers `away` and `offline`) is for
// programmatic transitions like the decay sweeper.
type SetPresenceStatusRequest struct {
	Status string `json:"status" validate:"required,oneof=online sleeping" example:"sleeping"`
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
