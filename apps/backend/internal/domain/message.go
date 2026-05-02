package domain

import (
	"time"

	"github.com/google/uuid"
)

// Message mirrors a row in the `messages` table (migration 0005). The
// generated `body_tsv` column is index-only — repos read body, never
// the tsvector.
//
// Soft-delete behavior (§4.6): deleted messages stay in conversation
// history with `body` blanked at the DTO converter; ID is preserved so
// the frontend can dedupe + render a "deleted message" placeholder.
// Repositories provide GetByIDIncludingDeleted for sender lookups so
// reading old conversations doesn't break.
type Message struct {
	ID               uuid.UUID
	ConversationID   uuid.UUID
	SenderID         uuid.UUID
	Body             string
	ReplyToMessageID *uuid.UUID
	CreatedAt        time.Time
	EditedAt         *time.Time
	DeletedAt        *time.Time
}

// IsEdited reports whether the message has been edited at least once.
func (m Message) IsEdited() bool { return m.EditedAt != nil }

// IsDeleted reports whether the message is soft-deleted.
func (m Message) IsDeleted() bool { return m.DeletedAt != nil }

// MessageRead mirrors a row in `message_reads`. Composite PK is
// (message_id, user_id) so a re-mark is idempotent.
type MessageRead struct {
	MessageID uuid.UUID
	UserID    uuid.UUID
	ReadAt    time.Time
}
