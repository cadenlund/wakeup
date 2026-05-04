package domain

import (
	"time"

	"github.com/google/uuid"
)

// ConversationType is the kind of conversation. Stored as a text column
// in the `conversations` table with a CHECK constraint matching these
// values exactly — keep wire shape lowercase per §4.6.
type ConversationType string

// Locked conversation types — must match the migration 0004 CHECK.
const (
	ConversationDirect ConversationType = "direct"
	ConversationGroup  ConversationType = "group"
)

// MemberRole is a member's role within a conversation. Match the §4.6
// rules: 'member' is the default; 'admin' can add/remove members.
type MemberRole string

// Locked member roles — must match the migration 0004 CHECK.
const (
	MemberRoleMember MemberRole = "member"
	MemberRoleAdmin  MemberRole = "admin"
)

// Conversation mirrors a row in the `conversations` table (migration
// 0004). Direct conversations have nil Name + AvatarURL; groups carry
// both.
type Conversation struct {
	ID            uuid.UUID
	Type          ConversationType
	Name          *string
	AvatarURL     *string
	CreatedBy     uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastMessageAt time.Time
}

// IsGroup reports whether the conversation is a group (3+ members).
func (c Conversation) IsGroup() bool { return c.Type == ConversationGroup }

// IsDirect reports whether the conversation is a 1:1 direct.
func (c Conversation) IsDirect() bool { return c.Type == ConversationDirect }

// ConversationMember mirrors a row in `conversation_members` (migration
// 0004). LastReadMessageID is nil until the user has read at least one
// message — handlers compare against the conversation's most-recent
// message id to compute unread counts.
type ConversationMember struct {
	ConversationID    uuid.UUID
	UserID            uuid.UUID
	Role              MemberRole
	JoinedAt          time.Time
	LastReadMessageID *uuid.UUID
	// MutedUntil is the per-member mute deadline. Non-nil + future =
	// push notifications suppressed for this conversation. WS events
	// still fire — only the push fanout gates on this.
	MutedUntil *time.Time
	// PinnedAt marks the conversation as pinned to the top of the
	// user's list. Non-nil = pinned; ordering among pinned is
	// pinned_at DESC (most recently pinned floats highest).
	PinnedAt *time.Time
}

// IsAdmin reports whether the member has admin privileges in the
// conversation (can add/remove other members per §4.6).
func (m ConversationMember) IsAdmin() bool { return m.Role == MemberRoleAdmin }
