package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// ConversationResponse is the wire shape for a single conversation row.
// `members` is the full participant list with embedded public profiles
// — frontend renders avatars + names from this without follow-up calls.
type ConversationResponse struct {
	ID            uuid.UUID               `json:"id"              example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Type          string                  `json:"type"            example:"group"`
	Name          *string                 `json:"name"            example:"Wakeup Crew"`
	AvatarURL     *string                 `json:"avatar_url"      example:"https://wakeup.app/avatars/group.png"`
	CreatedAt     time.Time               `json:"created_at"      example:"2026-05-02T09:31:21.810Z"`
	UpdatedAt     time.Time               `json:"updated_at"      example:"2026-05-02T09:35:11.221Z"`
	LastMessageAt time.Time               `json:"last_message_at" example:"2026-05-02T10:42:55.412Z"`
	Members       []ConversationMemberRow `json:"members"`
}

// ConversationMemberRow is one member of a ConversationResponse.
type ConversationMemberRow struct {
	User     UserResponse `json:"user"`
	Role     string       `json:"role"               example:"admin"`
	JoinedAt time.Time    `json:"joined_at"          example:"2026-05-02T09:31:21.810Z"`
}

// ConversationListResponse is the §6.4 paginated envelope for
// GET /v1/conversations. Members are included — keeps the list view
// renderable without N follow-up calls.
type ConversationListResponse struct {
	Data       []ConversationResponse `json:"data"`
	NextCursor *string                `json:"next_cursor"  example:"eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0="`
	HasMore    bool                   `json:"has_more"     example:"true"`
}

// CreateConversationRequest is the body for POST /v1/conversations.
//
// `direct` requires exactly one entry in `member_ids` (the other party).
// `group` requires `name` plus 1-24 entries in `member_ids` (creator
// adds to 25 total, the §4.6 cap).
type CreateConversationRequest struct {
	Type      string      `json:"type"                validate:"required,oneof=direct group" example:"group"`
	MemberIDs []uuid.UUID `json:"member_ids"          validate:"required,min=1,max=24"      example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Name      *string     `json:"name,omitempty"      validate:"omitempty,min=1,max=80"     example:"Wakeup Crew"`
	AvatarURL *string     `json:"avatar_url,omitempty" validate:"omitempty,url,max=2048"    example:"https://wakeup.app/avatars/group.png"`
}

// UpdateConversationRequest is the body for PATCH /v1/conversations/{id}.
// All fields optional; nil-means-unchanged.
type UpdateConversationRequest struct {
	Name      *string `json:"name,omitempty"       validate:"omitempty,min=1,max=80"  example:"Wakeup Crew"`
	AvatarURL *string `json:"avatar_url,omitempty" validate:"omitempty,url,max=2048" example:"https://wakeup.app/avatars/group.png"`
}

// AddMembersRequest is the body for POST /v1/conversations/{id}/members.
type AddMembersRequest struct {
	UserIDs []uuid.UUID `json:"user_ids" validate:"required,min=1,max=24" example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
}

// AddMembersResponse is returned on a successful members add.
type AddMembersResponse struct {
	Added []ConversationMemberRow `json:"added"`
}

// MarkReadRequest is the body for POST /v1/conversations/{id}/read.
type MarkReadRequest struct {
	UpToMessageID uuid.UUID `json:"up_to_message_id" validate:"required" example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
}

// toConversationMemberRow renders a single conversation_members row
// with the embedded counterparty profile.
func toConversationMemberRow(m domain.ConversationMember, u domain.User) ConversationMemberRow {
	return ConversationMemberRow{
		User:     toUserResponse(u),
		Role:     string(m.Role),
		JoinedAt: m.JoinedAt,
	}
}

// toConversationResponse renders a single conversation with members,
// pulling the embedded user records from a pre-loaded `usersByID` map
// (built by the handler so we do one batch SELECT per request).
func toConversationResponse(c domain.Conversation, members []domain.ConversationMember, usersByID map[uuid.UUID]domain.User) ConversationResponse {
	rows := make([]ConversationMemberRow, 0, len(members))
	for _, m := range members {
		u, ok := usersByID[m.UserID]
		if !ok {
			// FK cascade should make this impossible, but render the
			// §4.6 placeholder rather than a half-empty payload.
			u = domain.User{ID: m.UserID}
		}
		rows = append(rows, toConversationMemberRow(m, u))
	}
	return ConversationResponse{
		ID:            c.ID,
		Type:          string(c.Type),
		Name:          c.Name,
		AvatarURL:     c.AvatarURL,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
		LastMessageAt: c.LastMessageAt,
		Members:       rows,
	}
}
