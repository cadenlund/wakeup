// Package wsproto is the wire format every /v1/ws message rides on. The
// envelope and event-type registry come from §7 (realtime protocol).
//
// Wire shape (§7.1):
//
//	{ "type": "message.new", "data": { ... } }
//
// Producers call Encode(EventType, payload) → []byte and write to the
// connection. Consumers call Decode([]byte) → Envelope, check Type, then
// UnmarshalData(env, &target) to extract a typed payload.
//
// Decode rejects unknown EventTypes (returns ErrUnknownType) so a client
// sending a typo'd or future-only event lands in slog rather than silently
// being treated as a no-op.
package wsproto

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EventType is the wire identity of every WS event. Stable strings —
// renaming any of these is a frontend-breaking change.
type EventType string

// Server → client events (§7.2).
const (
	EventMessageNew                EventType = "message.new"
	EventMessageEdited             EventType = "message.edited"
	EventMessageDeleted            EventType = "message.deleted"
	EventMessageRead               EventType = "message.read"
	EventConversationCreated       EventType = "conversation.created"
	EventConversationUpdated       EventType = "conversation.updated"
	EventConversationMemberAdded   EventType = "conversation.member_added"
	EventConversationMemberRemoved EventType = "conversation.member_removed"
	EventPresenceUpdate            EventType = "presence.update"
	EventTypingStart               EventType = "typing.start"
	EventTypingStop                EventType = "typing.stop"
	EventFriendRequestReceived     EventType = "friend.request_received"
	EventFriendRequestAccepted     EventType = "friend.request_accepted"
	EventRoomStarted               EventType = "room.started"
	EventRoomParticipantJoined     EventType = "room.participant_joined"
	EventRoomParticipantLeft       EventType = "room.participant_left"
	EventRoomVideoChanged          EventType = "room.video_changed"
	EventRoomEnded                 EventType = "room.ended"
)

// Client → server events (§7.3).
const (
	EventHeartbeat   EventType = "heartbeat"
	EventPresenceSet EventType = "presence.set"
)

// Envelope is the §7.1 wire format. Data is held as json.RawMessage so
// Decode can validate the type before the caller commits to a payload
// schema — that lets Decode reject unknown types without doing work.
type Envelope struct {
	Type EventType       `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Sentinel errors returned by Decode + UnmarshalData. Callers compare with
// errors.Is to distinguish "client sent garbage" (drop the conn / log warn)
// from "I don't recognize this event" (also drop, but worth logging the
// type name so we can tell when a frontend ships a new event the backend
// hasn't been updated for yet).
var (
	ErrEmptyType     = errors.New("wsproto: envelope.type is empty")
	ErrUnknownType   = errors.New("wsproto: envelope.type is not a known event")
	ErrMalformed     = errors.New("wsproto: envelope is not valid JSON")
	ErrNoData        = errors.New("wsproto: envelope.data is missing or null")
	ErrPayloadShape  = errors.New("wsproto: payload shape did not match")
	ErrEmptyEnvelope = errors.New("wsproto: envelope is empty")
)

// knownEvents is the type-name registry. Updated alongside the const
// blocks above; tests assert AllEvents() matches the const list so adding
// a new EventType without registering it fails the suite.
var knownEvents = map[EventType]struct{}{
	EventMessageNew:                {},
	EventMessageEdited:             {},
	EventMessageDeleted:            {},
	EventMessageRead:               {},
	EventConversationCreated:       {},
	EventConversationUpdated:       {},
	EventConversationMemberAdded:   {},
	EventConversationMemberRemoved: {},
	EventPresenceUpdate:            {},
	EventTypingStart:               {},
	EventTypingStop:                {},
	EventFriendRequestReceived:     {},
	EventFriendRequestAccepted:     {},
	EventRoomStarted:               {},
	EventRoomParticipantJoined:     {},
	EventRoomParticipantLeft:       {},
	EventRoomVideoChanged:          {},
	EventRoomEnded:                 {},
	EventHeartbeat:                 {},
	EventPresenceSet:               {},
}

// AllEvents returns every registered EventType. Useful for tests + the WS
// hub's startup self-check.
func AllEvents() []EventType {
	out := make([]EventType, 0, len(knownEvents))
	for e := range knownEvents {
		out = append(out, e)
	}
	return out
}

// IsKnown reports whether t is a registered EventType.
func IsKnown(t EventType) bool {
	_, ok := knownEvents[t]
	return ok
}

// Encode marshals payload into the §7.1 envelope shape. Returns an error
// if t is not a known event type — wsproto refuses to emit something a
// future Decode would reject.
func Encode(t EventType, payload any) ([]byte, error) {
	if t == "" {
		return nil, ErrEmptyType
	}
	if !IsKnown(t) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, t)
	}
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("wsproto: marshal payload for %q: %w", t, err)
	}
	envBytes, err := json.Marshal(Envelope{Type: t, Data: dataBytes})
	if err != nil {
		return nil, fmt.Errorf("wsproto: marshal envelope for %q: %w", t, err)
	}
	return envBytes, nil
}

// Decode parses an envelope. Validates the type registry and rejects empty
// input. Returns Envelope so the caller can inspect Type before committing
// to a payload type for UnmarshalData.
func Decode(raw []byte) (Envelope, error) {
	if len(raw) == 0 {
		return Envelope{}, ErrEmptyEnvelope
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if env.Type == "" {
		return Envelope{}, ErrEmptyType
	}
	if !IsKnown(env.Type) {
		return Envelope{}, fmt.Errorf("%w: %q", ErrUnknownType, env.Type)
	}
	return env, nil
}

// UnmarshalData unmarshals env.Data into target (a pointer to the expected
// payload type). Returns ErrNoData if the envelope had no data field, or
// ErrPayloadShape on a JSON-shape mismatch.
func UnmarshalData(env Envelope, target any) error {
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return ErrNoData
	}
	if err := json.Unmarshal(env.Data, target); err != nil {
		return fmt.Errorf("%w: %w", ErrPayloadShape, err)
	}
	return nil
}

// --- Payload types -------------------------------------------------------
//
// Wire-shape structs for the events whose payloads aren't full domain
// types. Events that wrap a Message / Conversation / FriendRequest don't
// have a dedicated struct here — handlers marshal the response DTO directly
// via Encode(EventXxx, dto).

// MessageDeletedPayload — `message.deleted`.
type MessageDeletedPayload struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
}

// MessageEventPayload — `message.new` / `message.edited`. The ids let
// clients invalidate the right thread; `body` rides along so an
// in-app banner can show a preview without a fetch (a thread you're
// not on isn't loaded). Not the full Message DTO — attachments /
// reply_to / edited_at aren't needed by either consumer (the open
// thread refetches the page). `message.deleted` uses
// MessageDeletedPayload (no body to report).
type MessageEventPayload struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	SenderID       uuid.UUID `json:"sender_id"`
	CreatedAt      time.Time `json:"created_at"`
	Body           string    `json:"body"`
}

// MessageReadPayload — `message.read`. Fans out on the conversation's
// `conv:<id>:messages` channel (every member, not just the message
// sender) so each open thread can advance the reader's pointer for the
// §6.3 "Seen by …" captions. MessageID is the newest message the
// reader has now read — i.e. their last-read pointer.
type MessageReadPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	MessageID      uuid.UUID `json:"message_id"`
	UserID         uuid.UUID `json:"user_id"`
	ReadAt         time.Time `json:"read_at"`
}

// WSUser is the minimal user identity carried inside per-user events
// (friend / member-added) — just enough for an in-app notification.
// Built from domain.User by the publishing service; no avatar URL,
// since that needs presigning, which is a handler concern.
type WSUser struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
}

// ConversationCreatedPayload — `conversation.created`, fanned out on
// each initial member's `user:<id>:events`. Carries just the id: the
// client refetches the chats list off it, and the WS bridge uses it
// to subscribe the connection to the new `conv:<id>:messages` channel
// (otherwise a conversation created after connect wouldn't receive
// live message / typing events until the socket reconnected).
type ConversationCreatedPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
}

// ConversationMemberAddedPayload — `conversation.member_added`, fanned
// out on each member's `user:<id>:events`. `Member` is the user that
// was added; `ConversationName` is empty for an unnamed group. The WS
// bridge also subscribes the recipient's connection to the conv
// channel off this (so a freshly-added member gets live events).
type ConversationMemberAddedPayload struct {
	ConversationID   uuid.UUID `json:"conversation_id"`
	ConversationName string    `json:"conversation_name"`
	Member           WSUser    `json:"member"`
}

// ConversationMemberRemovedPayload — `conversation.member_removed`.
type ConversationMemberRemovedPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         uuid.UUID `json:"user_id"`
}

// FriendRequestEventPayload — `friend.request_received` (to the
// addressee; `User` = the requester) and `friend.request_accepted`
// (to the requester; `User` = the accepter).
type FriendRequestEventPayload struct {
	RequestID uuid.UUID `json:"request_id"`
	User      WSUser    `json:"user"`
}

// PresenceUpdatePayload — `presence.update`.
type PresenceUpdatePayload struct {
	UserID       uuid.UUID `json:"user_id"`
	Status       string    `json:"status"` // online|away|offline|sleeping
	LastActiveAt time.Time `json:"last_active_at"`
}

// TypingPayload — both `typing.start` and `typing.stop`. UserID is filled
// by the server on the S→C path; clients send only ConversationID on C→S.
type TypingPayload struct {
	ConversationID uuid.UUID  `json:"conversation_id"`
	UserID         *uuid.UUID `json:"user_id,omitempty"`
}

// RoomStartedPayload — `room.started`. Fired when first participant joins
// an empty conversation room.
type RoomStartedPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	InitiatorID    uuid.UUID `json:"initiator_id"`
	Video          bool      `json:"video"`
}

// RoomParticipantJoinedPayload — `room.participant_joined`.
type RoomParticipantJoinedPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         uuid.UUID `json:"user_id"`
	Video          bool      `json:"video"`
	JoinedAt       time.Time `json:"joined_at"`
}

// RoomParticipantLeftPayload — `room.participant_left`.
type RoomParticipantLeftPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         uuid.UUID `json:"user_id"`
}

// RoomVideoChangedPayload — `room.video_changed`.
type RoomVideoChangedPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	UserID         uuid.UUID `json:"user_id"`
	Video          bool      `json:"video"`
}

// RoomEndedPayload — `room.ended`. Fired when last participant leaves.
type RoomEndedPayload struct {
	ConversationID uuid.UUID `json:"conversation_id"`
}

// HeartbeatPayload — `heartbeat`.
//
// C→S (every 30s when foregrounded): the client sends the bare event;
// UnreadTotal is unset / ignored on the inbound direction.
//
// S→C (heartbeat ack): the server replies with the same event type
// carrying the user's current unread message total. Mobile uses this
// to keep the app-icon badge accurate (WAKEUPEXPO.md §7.5) without a
// REST round-trip per heartbeat.
type HeartbeatPayload struct {
	UnreadTotal int64 `json:"unread_total,omitempty"`
}

// PresenceSetPayload — `presence.set` (C→S manual override).
type PresenceSetPayload struct {
	Status string `json:"status"` // "online" or "sleeping"
}
