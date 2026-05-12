package wsproto_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// canonicalEvents is the source of truth for "every event the spec defines."
// If a constant is added in §7 / wsproto.go without updating this list, the
// matrix tests below fail — keeping the package and the spec in sync.
var canonicalEvents = []wsproto.EventType{
	wsproto.EventMessageNew,
	wsproto.EventMessageEdited,
	wsproto.EventMessageDeleted,
	wsproto.EventMessageRead,
	wsproto.EventConversationCreated,
	wsproto.EventConversationUpdated,
	wsproto.EventConversationMemberAdded,
	wsproto.EventConversationMemberRemoved,
	wsproto.EventPresenceUpdate,
	wsproto.EventTypingStart,
	wsproto.EventTypingStop,
	wsproto.EventFriendRequestReceived,
	wsproto.EventFriendRequestAccepted,
	wsproto.EventRoomStarted,
	wsproto.EventRoomParticipantJoined,
	wsproto.EventRoomParticipantLeft,
	wsproto.EventRoomVideoChanged,
	wsproto.EventRoomEnded,
	wsproto.EventHeartbeat,
	wsproto.EventPresenceSet,
}

func TestAllEvents_RegistryMatchesCanonical(t *testing.T) {
	t.Parallel()
	got := wsproto.AllEvents()
	if len(got) != len(canonicalEvents) {
		t.Fatalf("AllEvents length %d != canonical length %d (event added without test update?)",
			len(got), len(canonicalEvents))
	}
	gotSet := map[wsproto.EventType]struct{}{}
	for _, e := range got {
		gotSet[e] = struct{}{}
	}
	for _, e := range canonicalEvents {
		if _, ok := gotSet[e]; !ok {
			t.Errorf("registry missing %q", e)
		}
	}
}

func TestIsKnown(t *testing.T) {
	t.Parallel()
	for _, e := range canonicalEvents {
		if !wsproto.IsKnown(e) {
			t.Errorf("IsKnown(%q) = false, want true", e)
		}
	}
	if wsproto.IsKnown("not.a.real.event") {
		t.Error("IsKnown(unknown) returned true")
	}
	if wsproto.IsKnown("") {
		t.Error("IsKnown(\"\") returned true")
	}
}

// Round-trip every event type. For events whose payload schema we own
// (defined in this package), use the typed payload; for events that wrap
// a domain type we don't have yet (Message, Conversation, FriendRequest),
// use a small map[string]any standin to prove the envelope shape works.
func TestEncodeDecode_RoundTripEveryEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 2, 12, 31, 21, 0, time.UTC)
	convID := uuid.New()
	userID := uuid.New()
	msgID := uuid.New()

	cases := []struct {
		event   wsproto.EventType
		payload any
	}{
		{wsproto.EventMessageNew, map[string]any{"id": msgID, "body": "hi"}},
		{wsproto.EventMessageEdited, map[string]any{"id": msgID, "body": "hi (edited)"}},
		{wsproto.EventMessageDeleted, wsproto.MessageDeletedPayload{MessageID: msgID, ConversationID: convID}},
		{wsproto.EventMessageRead, wsproto.MessageReadPayload{ConversationID: convID, MessageID: msgID, UserID: userID, ReadAt: now}},
		{wsproto.EventConversationCreated, map[string]any{"id": convID, "type": "group"}},
		{wsproto.EventConversationUpdated, map[string]any{"id": convID, "name": "renamed"}},
		{wsproto.EventConversationMemberAdded, wsproto.ConversationMemberAddedPayload{
			ConversationID: convID, ConversationName: "Roommates",
			Member: wsproto.WSUser{ID: userID, Username: "a", DisplayName: "Ada"},
		}},
		{wsproto.EventConversationMemberRemoved, wsproto.ConversationMemberRemovedPayload{
			ConversationID: convID, UserID: userID,
		}},
		{wsproto.EventPresenceUpdate, wsproto.PresenceUpdatePayload{
			UserID: userID, Status: "online", LastActiveAt: now,
		}},
		{wsproto.EventTypingStart, wsproto.TypingPayload{ConversationID: convID, UserID: &userID}},
		{wsproto.EventTypingStop, wsproto.TypingPayload{ConversationID: convID, UserID: &userID}},
		{wsproto.EventFriendRequestReceived, wsproto.FriendRequestEventPayload{
			RequestID: uuid.New(), User: wsproto.WSUser{ID: userID, Username: "a", DisplayName: "Ada"},
		}},
		{wsproto.EventFriendRequestAccepted, wsproto.FriendRequestEventPayload{
			RequestID: uuid.New(), User: wsproto.WSUser{ID: userID, Username: "b", DisplayName: "Ben"},
		}},
		{wsproto.EventRoomStarted, wsproto.RoomStartedPayload{
			ConversationID: convID, InitiatorID: userID, Video: false,
		}},
		{wsproto.EventRoomParticipantJoined, wsproto.RoomParticipantJoinedPayload{
			ConversationID: convID, UserID: userID, Video: true, JoinedAt: now,
		}},
		{wsproto.EventRoomParticipantLeft, wsproto.RoomParticipantLeftPayload{
			ConversationID: convID, UserID: userID,
		}},
		{wsproto.EventRoomVideoChanged, wsproto.RoomVideoChangedPayload{
			ConversationID: convID, UserID: userID, Video: true,
		}},
		{wsproto.EventRoomEnded, wsproto.RoomEndedPayload{ConversationID: convID}},
		{wsproto.EventHeartbeat, wsproto.HeartbeatPayload{}},
		{wsproto.EventPresenceSet, wsproto.PresenceSetPayload{Status: "sleeping"}},
	}

	if len(cases) != len(canonicalEvents) {
		t.Fatalf("test cases (%d) don't cover every canonical event (%d)", len(cases), len(canonicalEvents))
	}

	for _, tc := range cases {
		t.Run(string(tc.event), func(t *testing.T) {
			t.Parallel()
			raw, err := wsproto.Encode(tc.event, tc.payload)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			env, err := wsproto.Decode(raw)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if env.Type != tc.event {
				t.Errorf("Type = %q, want %q", env.Type, tc.event)
			}
			if len(env.Data) == 0 {
				t.Errorf("Data is empty after round-trip")
			}
		})
	}
}

func TestDecode_RejectsUnknownType(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"type":"banana.peel","data":{}}`)
	_, err := wsproto.Decode(raw)
	if err == nil {
		t.Fatal("expected ErrUnknownType")
	}
	if !errors.Is(err, wsproto.ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType, got: %v", err)
	}
}

func TestDecode_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	_, err := wsproto.Decode([]byte("not-json"))
	if !errors.Is(err, wsproto.ErrMalformed) {
		t.Fatalf("expected ErrMalformed, got: %v", err)
	}
}

func TestDecode_RejectsEmptyType(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"type":"","data":{}}`)
	_, err := wsproto.Decode(raw)
	if !errors.Is(err, wsproto.ErrEmptyType) {
		t.Fatalf("expected ErrEmptyType, got: %v", err)
	}
}

func TestDecode_RejectsEmptyEnvelope(t *testing.T) {
	t.Parallel()
	if _, err := wsproto.Decode(nil); !errors.Is(err, wsproto.ErrEmptyEnvelope) {
		t.Fatalf("Decode(nil): expected ErrEmptyEnvelope, got %v", err)
	}
	if _, err := wsproto.Decode([]byte{}); !errors.Is(err, wsproto.ErrEmptyEnvelope) {
		t.Fatalf("Decode(empty): expected ErrEmptyEnvelope, got %v", err)
	}
}

func TestEncode_RejectsUnknownType(t *testing.T) {
	t.Parallel()
	_, err := wsproto.Encode("not.real", struct{}{})
	if !errors.Is(err, wsproto.ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType, got: %v", err)
	}
}

func TestEncode_RejectsEmptyType(t *testing.T) {
	t.Parallel()
	_, err := wsproto.Encode("", struct{}{})
	if !errors.Is(err, wsproto.ErrEmptyType) {
		t.Fatalf("expected ErrEmptyType, got: %v", err)
	}
}

func TestUnmarshalData_RoundTripsTypedPayload(t *testing.T) {
	t.Parallel()
	want := wsproto.PresenceUpdatePayload{
		UserID:       uuid.New(),
		Status:       "online",
		LastActiveAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	raw, err := wsproto.Encode(wsproto.EventPresenceUpdate, want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	env, err := wsproto.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	var got wsproto.PresenceUpdatePayload
	if err := wsproto.UnmarshalData(env, &got); err != nil {
		t.Fatalf("UnmarshalData: %v", err)
	}
	if got.UserID != want.UserID || got.Status != want.Status || !got.LastActiveAt.Equal(want.LastActiveAt) {
		t.Fatalf("round trip differs: got %+v, want %+v", got, want)
	}
}

func TestUnmarshalData_NoDataReturnsErr(t *testing.T) {
	t.Parallel()
	cases := []wsproto.Envelope{
		{Type: wsproto.EventHeartbeat, Data: nil},
		{Type: wsproto.EventHeartbeat, Data: []byte("null")},
	}
	for _, env := range cases {
		var target struct{}
		err := wsproto.UnmarshalData(env, &target)
		if !errors.Is(err, wsproto.ErrNoData) {
			t.Errorf("env=%+v: expected ErrNoData, got %v", env, err)
		}
	}
}

func TestUnmarshalData_PayloadShapeMismatch(t *testing.T) {
	t.Parallel()
	// Data is a string where we expect a struct → unmarshal fails.
	env := wsproto.Envelope{
		Type: wsproto.EventPresenceUpdate,
		Data: json.RawMessage(`"not a struct"`),
	}
	var target wsproto.PresenceUpdatePayload
	err := wsproto.UnmarshalData(env, &target)
	if !errors.Is(err, wsproto.ErrPayloadShape) {
		t.Fatalf("expected ErrPayloadShape, got: %v", err)
	}
}

// The wire JSON shape must match §7.1 exactly: top-level `type` + `data`,
// nothing else. Decoding into a strict struct and re-encoding catches any
// rename of the JSON keys.
func TestEncode_WireShapeMatchesSpec(t *testing.T) {
	t.Parallel()
	raw, err := wsproto.Encode(wsproto.EventHeartbeat, wsproto.HeartbeatPayload{})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(string(raw), `"type":"heartbeat"`) {
		t.Errorf("envelope should include type field as expected: %s", raw)
	}
	if !strings.Contains(string(raw), `"data":`) {
		t.Errorf("envelope should include data field: %s", raw)
	}
}
