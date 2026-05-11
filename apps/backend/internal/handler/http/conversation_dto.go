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
	ID            uuid.UUID `json:"id"              example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Type          string    `json:"type"            example:"group"`
	Name          *string   `json:"name"            example:"Wakeup Crew"`
	AvatarURL     *string   `json:"avatar_url"      example:"https://wakeup.app/avatars/group.png"`
	CreatedAt     time.Time `json:"created_at"      example:"2026-05-02T09:31:21.810Z"`
	UpdatedAt     time.Time `json:"updated_at"      example:"2026-05-02T09:35:11.221Z"`
	LastMessageAt time.Time `json:"last_message_at" example:"2026-05-02T10:42:55.412Z"`
	// MutedUntil + PinnedAt are the CALLER's membership state, not
	// shared. Other members may have different mute / pin values for
	// the same conversation. Surfaced here so the list and detail
	// responses give the client everything it needs to render mute
	// icons and sort pinned-first without follow-up calls.
	MutedUntil *time.Time              `json:"muted_until"     example:"2026-05-02T18:00:00Z"`
	PinnedAt   *time.Time              `json:"pinned_at"       example:"2026-05-02T09:31:21.810Z"`
	Members    []ConversationMemberRow `json:"members"`
}

// ConversationMemberRow is one member of a ConversationResponse.
//
// LastReadMessageID is the per-member read pointer maintained by
// `POST /v1/conversations/{id}/read` (§6.3). Mobile renders tiny
// read-receipt avatars under "mine" bubbles in groups by comparing
// this id against each rendered message id. JSON-null (not omitted)
// when the member has never opened the thread — stable nullability
// is part of the contract so clients can distinguish "never read"
// from a missing field on an older response (CR on PR #143).
type ConversationMemberRow struct {
	User              UserResponse `json:"user"`
	Role              string       `json:"role"                 example:"admin"`
	JoinedAt          time.Time    `json:"joined_at"            example:"2026-05-02T09:31:21.810Z"`
	LastReadMessageID *uuid.UUID   `json:"last_read_message_id" example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
}

// ConversationListResponse is the §6.4 paginated envelope for
// GET /v1/conversations. Members are included — keeps the list view
// renderable without N follow-up calls. Total is the absolute
// conversation count across every page so the UI can render
// "showing N of M" hints without paging through every cursor.
type ConversationListResponse struct {
	Data       []ConversationResponse `json:"data"`
	Total      int                    `json:"total"        example:"42"`
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

// SetMuteRequest is the body for PATCH /v1/conversations/{id}/mute.
// `until = null` unmutes; a future timestamp suppresses pushes until
// then. "Forever" is just a far-future stamp like 2099-01-01.
type SetMuteRequest struct {
	Until *time.Time `json:"until" example:"2026-05-02T18:00:00Z"`
}

// SetPinRequest is the body for PATCH /v1/conversations/{id}/pin.
// Server stamps `pinned_at = now()` when true, NULL when false.
//
// `Pinned` is a pointer so we can distinguish an omitted field from an
// explicit `false`. validator/v10's `required` on a non-pointer bool
// rejects `false`, which would mean callers couldn't unpin.
type SetPinRequest struct {
	Pinned *bool `json:"pinned" validate:"required" example:"true"`
}

// ConversationMemberResponse is the wire shape returned by
// PATCH /v1/conversations/{id}/{mute,pin}. Includes everything the
// client needs to update its local cached row.
type ConversationMemberResponse struct {
	ConversationID    uuid.UUID  `json:"conversation_id"   example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	UserID            uuid.UUID  `json:"user_id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Role              string     `json:"role"              example:"member"`
	JoinedAt          time.Time  `json:"joined_at"         example:"2026-05-02T09:31:21.810Z"`
	LastReadMessageID *uuid.UUID `json:"last_read_message_id" example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	MutedUntil        *time.Time `json:"muted_until"       example:"2026-05-02T18:00:00Z"`
	PinnedAt          *time.Time `json:"pinned_at"         example:"2026-05-02T09:31:21.810Z"`
}

func toConversationMemberResponse(m domain.ConversationMember) ConversationMemberResponse {
	return ConversationMemberResponse{
		ConversationID:    m.ConversationID,
		UserID:            m.UserID,
		Role:              string(m.Role),
		JoinedAt:          m.JoinedAt,
		LastReadMessageID: m.LastReadMessageID,
		MutedUntil:        m.MutedUntil,
		PinnedAt:          m.PinnedAt,
	}
}

// toConversationMemberRow renders a single conversation_members row
// with the embedded counterparty profile.
func toConversationMemberRow(m domain.ConversationMember, u domain.User, p Presigner) ConversationMemberRow {
	return ConversationMemberRow{
		User:              toUserResponse(u, p),
		Role:              string(m.Role),
		JoinedAt:          m.JoinedAt,
		LastReadMessageID: m.LastReadMessageID,
	}
}

// toConversationResponse renders a single conversation with members,
// pulling the embedded user records from a pre-loaded `usersByID` map
// (built by the handler so we do one batch SELECT per request).
//
// callerID identifies which member row carries the per-caller mute and
// pin state — those fields are surfaced at the top level so the
// client can render a mute icon / sort pinned-first without scanning
// the embedded members slice.
func toConversationResponse(c domain.Conversation, callerID uuid.UUID, members []domain.ConversationMember, usersByID map[uuid.UUID]domain.User, p Presigner) ConversationResponse {
	rows := make([]ConversationMemberRow, 0, len(members))
	var mutedUntil, pinnedAt *time.Time
	for _, m := range members {
		u, ok := usersByID[m.UserID]
		if !ok {
			// FK cascade should make this impossible, but render the
			// §4.6 placeholder rather than a half-empty payload.
			u = domain.User{ID: m.UserID}
		}
		rows = append(rows, toConversationMemberRow(m, u, p))
		if m.UserID == callerID {
			mutedUntil = m.MutedUntil
			pinnedAt = m.PinnedAt
		}
	}
	return ConversationResponse{
		ID:            c.ID,
		Type:          string(c.Type),
		Name:          c.Name,
		AvatarURL:     c.AvatarURL,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
		LastMessageAt: c.LastMessageAt,
		MutedUntil:    mutedUntil,
		PinnedAt:      pinnedAt,
		Members:       rows,
	}
}
