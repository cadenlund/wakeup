package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// FriendshipResponse is the wire shape for a single friendship row. The
// embedded `other` UserResponse renders the counterparty (the user that
// ISN'T the caller) so the frontend doesn't have to branch on direction.
type FriendshipResponse struct {
	ID         uuid.UUID    `json:"id"                    example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Status     string       `json:"status"                example:"accepted"`
	Other      UserResponse `json:"user"`
	CreatedAt  time.Time    `json:"created_at"            example:"2026-05-02T09:31:21.810Z"`
	AcceptedAt *time.Time   `json:"accepted_at,omitempty" example:"2026-05-02T09:35:11.221Z"`
}

// FriendListResponse is the §6.4 paginated envelope for GET /v1/friends.
// Total is the absolute friend count across every page so the UI can
// render "showing N of M" hints and trigger drill-downs without paging
// through every cursor.
type FriendListResponse struct {
	Data       []FriendshipResponse `json:"data"`
	Total      int                  `json:"total"        example:"42"`
	NextCursor *string              `json:"next_cursor"  example:"eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0="`
	HasMore    bool                 `json:"has_more"     example:"true"`
}

// FriendRequestsResponse is the wire shape for GET /v1/friends/requests.
// Two arrays — incoming (someone requested me) and outgoing (I requested
// someone). The frontend renders them under separate tabs.
type FriendRequestsResponse struct {
	Incoming []FriendshipResponse `json:"incoming"`
	Outgoing []FriendshipResponse `json:"outgoing"`
}

// BlockListResponse is the wire shape for GET /v1/blocks. Returns the
// public profiles of users the caller has blocked. Unlike the friends
// list, this isn't paginated — typical block lists are small (<100)
// and the mobile settings/blocked screen wants the full set.
type BlockListResponse struct {
	Data []UserResponse `json:"data"`
}

// SendFriendRequestRequest is the body for POST /v1/friends/requests.
type SendFriendRequestRequest struct {
	Username string `json:"username" validate:"required,min=3,max=32,alphanum" example:"baron"`
}

// toFriendshipResponse renders a single friendship with the supplied
// counterparty user as the embedded `Other` field. Caller is responsible
// for resolving the right counterparty (Friendship.OtherID handles the
// branch on direction).
func toFriendshipResponse(f domain.Friendship, otherUser domain.User, p Presigner) FriendshipResponse {
	return FriendshipResponse{
		ID:         f.ID,
		Status:     string(f.Status),
		Other:      toUserResponse(otherUser, p),
		CreatedAt:  f.CreatedAt,
		AcceptedAt: f.AcceptedAt,
	}
}
