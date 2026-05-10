package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// MessageResponse is the wire shape for a single message row. Soft-
// deleted rows are still returned by GET /v1/conversations/{id}/messages
// (so the §4.6 placeholder can render in history) — body is blanked at
// this DTO boundary, sender_id is preserved.
type MessageResponse struct {
	ID               uuid.UUID  `json:"id"                          example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	ConversationID   uuid.UUID  `json:"conversation_id"             example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	SenderID         uuid.UUID  `json:"sender_id"                   example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Body             string     `json:"body"                        example:"hello world"`
	ReplyToMessageID *uuid.UUID `json:"reply_to_message_id"         example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	CreatedAt        time.Time  `json:"created_at"                  example:"2026-05-02T10:42:55.412Z"`
	EditedAt         *time.Time `json:"edited_at"                   example:"2026-05-02T10:43:11.221Z"`
	DeletedAt        *time.Time `json:"deleted_at"                  example:"2026-05-02T10:44:00.000Z"`
	IsDeleted        bool       `json:"is_deleted"                  example:"false"`
}

// MessageListResponse is the §6.4 paginated envelope for
// GET /v1/conversations/{id}/messages. Total is the absolute
// non-deleted message count (matching the `q` filter when set) across
// every page so the UI can render "showing N of M" hints without
// paging through every cursor.
type MessageListResponse struct {
	Data       []MessageResponse `json:"data"`
	Total      int               `json:"total"        example:"42"`
	NextCursor *string           `json:"next_cursor"  example:"eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0="`
	HasMore    bool              `json:"has_more"     example:"true"`
}

// SendMessageRequest is the body for POST /v1/conversations/{id}/messages.
//
// `body` is required (1-10000 chars after trim guard); `attachment_ids`
// must reference attachments uploaded by the caller (FK rejects bad IDs
// at insert); `reply_to_message_id` must live in the same conversation.
//
// Validator tag note: `attachment_ids` deliberately omits a `dive,required`
// element check. The `required` token there made swag's struct-tag
// analyzer mark the whole slice field as required in the OpenAPI schema
// (CodeRabbit caught this on PR #40); the schema's `message_attachments`
// FK already rejects bogus UUIDs at insert time.
type SendMessageRequest struct {
	Body             string      `json:"body"                              validate:"required,min=1,max=10000" example:"hello world"`
	AttachmentIDs    []uuid.UUID `json:"attachment_ids,omitempty"          validate:"omitempty,max=10"         example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	ReplyToMessageID *uuid.UUID  `json:"reply_to_message_id,omitempty"     validate:"omitempty"                example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
}

// EditMessageRequest is the body for PATCH /v1/messages/{id}.
type EditMessageRequest struct {
	Body string `json:"body" validate:"required,min=1,max=10000" example:"updated body"`
}

// MessageReadRow is one row of GET /v1/messages/{id}/reads.
type MessageReadRow struct {
	UserID uuid.UUID `json:"user_id"  example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	ReadAt time.Time `json:"read_at"  example:"2026-05-02T10:43:11.221Z"`
}

// MessageReadsResponse is the wire shape for GET /v1/messages/{id}/reads.
type MessageReadsResponse struct {
	Data []MessageReadRow `json:"data"`
}

// toMessageResponse converts a domain.Message into the wire shape.
// Soft-deleted rows have their body blanked here so the history endpoint
// can still surface placeholders without leaking content.
func toMessageResponse(m domain.Message) MessageResponse {
	body := m.Body
	if m.IsDeleted() {
		body = ""
	}
	return MessageResponse{
		ID:               m.ID,
		ConversationID:   m.ConversationID,
		SenderID:         m.SenderID,
		Body:             body,
		ReplyToMessageID: m.ReplyToMessageID,
		CreatedAt:        m.CreatedAt,
		EditedAt:         m.EditedAt,
		DeletedAt:        m.DeletedAt,
		IsDeleted:        m.IsDeleted(),
	}
}

// toMessageList converts a slice of domain.Message rows into wire shapes.
func toMessageList(msgs []domain.Message) []MessageResponse {
	out := make([]MessageResponse, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toMessageResponse(m))
	}
	return out
}

// toMessageReadRow converts a domain.MessageRead into a wire row.
func toMessageReadRow(r domain.MessageRead) MessageReadRow {
	return MessageReadRow{UserID: r.UserID, ReadAt: r.ReadAt}
}

// toMessageReadList converts a slice of MessageRead rows.
func toMessageReadList(rs []domain.MessageRead) []MessageReadRow {
	out := make([]MessageReadRow, 0, len(rs))
	for _, r := range rs {
		out = append(out, toMessageReadRow(r))
	}
	return out
}
