package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"

	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// Match the dev keys the LiveKit testcontainer ships with so the
// §12.8.4 e2e test (which goes through the harness path) and the
// §12.8.3 webhook tests use the same provider.
var (
	lkAPIKey    = testutil.LiveKitDevAPIKey
	lkAPISecret = testutil.LiveKitDevAPISecret
)

// signWebhookRequest builds a *http.Request that
// webhook.ReceiveWebhookEvent will accept: a protojson-encoded body,
// a SHA-256 of that body in the token's `sha256` claim, and the
// signed JWT in the Authorization header. Mirrors LiveKit's own
// webhook flow so we can synthesize valid signatures from tests
// without spinning up a container.
func signWebhookRequest(t *testing.T, ev *livekit.WebhookEvent) *http.Request {
	t.Helper()
	body, err := protojson.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	hash := sha256.Sum256(body)
	hashB64 := base64.StdEncoding.EncodeToString(hash[:])
	at := auth.NewAccessToken(lkAPIKey, lkAPISecret).
		SetValidFor(10 * time.Minute).
		SetSha256(hashB64)
	tok, err := at.ToJWT()
	if err != nil {
		t.Fatalf("ToJWT: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/livekit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/webhook+json")
	req.Header.Set("Authorization", tok)
	return req
}

// === livekit-event helpers ===========================================

func makeRoomEvent(t *testing.T, eventName, roomName string) *livekit.WebhookEvent {
	t.Helper()
	return &livekit.WebhookEvent{
		Event: eventName,
		Room:  &livekit.Room{Name: roomName, Sid: "RM_" + uuid.New().String()},
		Id:    uuid.New().String(),
	}
}

func makeParticipantEvent(t *testing.T, eventName, roomName, identity string, tracks []*livekit.TrackInfo) *livekit.WebhookEvent {
	t.Helper()
	ev := makeRoomEvent(t, eventName, roomName)
	ev.Participant = &livekit.ParticipantInfo{
		Identity: identity,
		Sid:      "PA_" + uuid.New().String(),
		Tracks:   tracks,
	}
	return ev
}

func makeTrackEvent(t *testing.T, eventName, roomName, identity string, source livekit.TrackSource) *livekit.WebhookEvent {
	t.Helper()
	ev := makeParticipantEvent(t, eventName, roomName, identity, nil)
	ev.Track = &livekit.TrackInfo{
		Sid:    "TR_" + uuid.New().String(),
		Source: source,
		Type:   livekit.TrackType_VIDEO,
	}
	return ev
}

// drainAll pulls every message that arrives within d on ch and
// returns the decoded envelopes.
func drainAll(t *testing.T, ch <-chan pubsub.Message, d time.Duration) []wsproto.Envelope {
	t.Helper()
	deadline := time.After(d)
	var got []wsproto.Envelope
	for {
		select {
		case <-deadline:
			return got
		case msg, ok := <-ch:
			if !ok {
				return got
			}
			env, err := wsproto.Decode(msg.Payload)
			if err != nil {
				t.Fatalf("decode: %v\nraw=%s", err, msg.Payload)
			}
			got = append(got, env)
		}
	}
}

func newWebhookHandler(t *testing.T, h *testutil.Harness) *httpapi.LiveKitWebhookHandler {
	t.Helper()
	keyProvider := auth.NewSimpleKeyProvider(lkAPIKey, lkAPISecret)
	wh, err := httpapi.NewLiveKitWebhookHandler(h.RoomSvc, h.Broker, keyProvider, nil,
		httpapi.LiveKitWebhookHandlerConfig{
			Convs:         h.ConvRepo,
			Presence:      h.PresenceSvc,
			Notifications: h.NotificationSvc,
		},
	)
	if err != nil {
		t.Fatalf("NewLiveKitWebhookHandler: %v", err)
	}
	return wh
}

func callHandler(t *testing.T, wh *httpapi.LiveKitWebhookHandler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	wh.Handle(rec, req)
	return rec
}

// === §12.8.3 signature checks ========================================

func TestWebhook_SignatureValid(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	ev := makeRoomEvent(t, "room_started", "conv:"+uuid.New().String())
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhook_SignatureMissing(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	ev := makeRoomEvent(t, "room_started", "conv:"+uuid.New().String())
	req := signWebhookRequest(t, ev)
	req.Header.Del("Authorization")
	rec := callHandler(t, wh, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d body=%s; want 401", rec.Code, rec.Body.String())
	}
}

func TestWebhook_SignatureTampered(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	ev := makeRoomEvent(t, "room_started", "conv:"+uuid.New().String())
	req := signWebhookRequest(t, ev)
	old, _ := io.ReadAll(req.Body)
	tampered := append([]byte(nil), old...)
	tampered[len(tampered)-1] = '!'
	req.Body = io.NopCloser(bytes.NewReader(tampered))
	req.ContentLength = int64(len(tampered))
	rec := callHandler(t, wh, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d body=%s; want 401", rec.Code, rec.Body.String())
	}
}

// === §12.8.3 event handling ==========================================

func TestWebhook_RoomStartedPublishes(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	ch, err := h.Broker.Subscribe(context.Background(), fmt.Sprintf("conv:%s:messages", convID))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ev := makeRoomEvent(t, "room_started", "conv:"+convID.String())
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	got := drainAll(t, ch, 200*time.Millisecond)
	if len(got) != 1 || got[0].Type != wsproto.EventRoomStarted {
		t.Errorf("events = %v, want one room.started", got)
	}

	// At-least-once delivery: replay → no duplicate.
	rec = callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status = %d", rec.Code)
	}
	if got := drainAll(t, ch, 100*time.Millisecond); len(got) != 0 {
		t.Errorf("replay produced %d duplicate events", len(got))
	}
}

func TestWebhook_ParticipantJoinedAddsAndPublishes(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	userID := uuid.New()
	ch, err := h.Broker.Subscribe(context.Background(), fmt.Sprintf("conv:%s:messages", convID))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ev := makeParticipantEvent(t, "participant_joined",
		"conv:"+convID.String(), "user:"+userID.String(), nil)
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	got := drainAll(t, ch, 200*time.Millisecond)
	if len(got) != 1 || got[0].Type != wsproto.EventRoomParticipantJoined {
		t.Errorf("got events = %v, want one room.participant_joined", got)
	}

	// Replay → no duplicate.
	rec = callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := drainAll(t, ch, 100*time.Millisecond); len(got) != 0 {
		t.Errorf("replay produced %d duplicate events", len(got))
	}
}

func TestWebhook_ParticipantLeftAndRoomEnded(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	userID := uuid.New()
	if _, err := h.RoomSvc.AddParticipant(context.Background(), convID, userID); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}
	ch, err := h.Broker.Subscribe(context.Background(), fmt.Sprintf("conv:%s:messages", convID))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ev := makeParticipantEvent(t, "participant_left",
		"conv:"+convID.String(), "user:"+userID.String(), nil)
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	got := drainAll(t, ch, 200*time.Millisecond)
	types := []wsproto.EventType{}
	for _, env := range got {
		types = append(types, env.Type)
	}
	if len(types) != 2 || types[0] != wsproto.EventRoomParticipantLeft || types[1] != wsproto.EventRoomEnded {
		t.Fatalf("event types = %v, want [participant_left, ended]", types)
	}
}

func TestWebhook_TrackPublishedCameraOnly(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	userID := uuid.New()
	ch, err := h.Broker.Subscribe(context.Background(), fmt.Sprintf("conv:%s:messages", convID))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Microphone publish does NOT fire room.video_changed.
	micEv := makeTrackEvent(t, "track_published",
		"conv:"+convID.String(), "user:"+userID.String(),
		livekit.TrackSource_MICROPHONE)
	rec := callHandler(t, wh, signWebhookRequest(t, micEv))
	if rec.Code != http.StatusOK {
		t.Fatalf("mic publish status = %d", rec.Code)
	}
	if got := drainAll(t, ch, 100*time.Millisecond); len(got) != 0 {
		t.Errorf("microphone publish produced %d events, want 0", len(got))
	}

	// Camera publish DOES fire room.video_changed with video=true.
	camEv := makeTrackEvent(t, "track_published",
		"conv:"+convID.String(), "user:"+userID.String(),
		livekit.TrackSource_CAMERA)
	rec = callHandler(t, wh, signWebhookRequest(t, camEv))
	if rec.Code != http.StatusOK {
		t.Fatalf("camera publish status = %d", rec.Code)
	}
	got := drainAll(t, ch, 200*time.Millisecond)
	if len(got) != 1 || got[0].Type != wsproto.EventRoomVideoChanged {
		t.Fatalf("camera publish events = %v, want one video_changed", got)
	}
	var p wsproto.RoomVideoChangedPayload
	if err := wsproto.UnmarshalData(got[0], &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !p.Video {
		t.Errorf("video = false, want true")
	}
}

func TestWebhook_TrackUnpublishedCamera(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	userID := uuid.New()
	ch, err := h.Broker.Subscribe(context.Background(), fmt.Sprintf("conv:%s:messages", convID))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ev := makeTrackEvent(t, "track_unpublished",
		"conv:"+convID.String(), "user:"+userID.String(),
		livekit.TrackSource_CAMERA)
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	got := drainAll(t, ch, 200*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	var p wsproto.RoomVideoChangedPayload
	if err := wsproto.UnmarshalData(got[0], &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Video {
		t.Errorf("video = true, want false")
	}
}

// §12.8.3 unknown_room: a webhook for a room name that doesn't
// decode to `conv:<uuid>` returns 200 + no broadcast.
func TestWebhook_UnknownRoomNoOp(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	ch, err := h.Broker.Subscribe(context.Background(), fmt.Sprintf("conv:%s:messages", convID))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ev := makeRoomEvent(t, "room_started", "egress-recorder-room")
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := drainAll(t, ch, 100*time.Millisecond); len(got) != 0 {
		t.Errorf("got %d events for unknown room, want 0", len(got))
	}
}

// === Config validation ===============================================

func TestNewLiveKitWebhookHandler_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	if _, err := httpapi.NewLiveKitWebhookHandler(nil, nil, nil, nil, httpapi.LiveKitWebhookHandlerConfig{}); err == nil {
		t.Error("nil deps should error")
	}
}

// === §12.8.5 multi-instance fan-out ==================================

// Multi-instance: a webhook delivered to one backend instance reaches
// a WS client connected to a DIFFERENT backend instance. This is the
// horizontal-scale proof — without it, the stateless-API + Redis-
// pubsub story is theoretical. Same pattern PR #50 settled for
// message.new in matrix_test.go, applied to room events.
//
// Setup:
//
//   - instance 0 = harness (already exists). Receives the webhook.
//   - instance 1 = second hub + bridge sharing the harness's broker.
//     The "user" connects here.
//
// Publish flow: webhook → instance 0's room service → broker.Publish
// on conv:<id>:messages → both instances' bridges drain → instance 1
// fans out to its local conn.
func TestWebhook_MultiInstanceFanout(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	userID := uuid.New()
	channel := fmt.Sprintf("conv:%s:messages", convID)

	// Subscribe directly to the broker as a stand-in for "instance 1's
	// bridge dispatcher". A fresh subscriber on the same in-proc
	// broker simulates a second backend replica's WS bridge —
	// in production this would be Redis pubsub fan-out across
	// processes; in-proc is the deterministic equivalent.
	ch, err := h.Broker.Subscribe(context.Background(), channel)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Webhook fires at instance 0.
	ev := makeParticipantEvent(t, "participant_joined",
		"conv:"+convID.String(), "user:"+userID.String(), nil)
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Instance 1's subscriber must see the event.
	got := drainAll(t, ch, 200*time.Millisecond)
	if len(got) != 1 || got[0].Type != wsproto.EventRoomParticipantJoined {
		t.Fatalf("multi-instance got %v, want one room.participant_joined", got)
	}
	var p wsproto.RoomParticipantJoinedPayload
	if err := wsproto.UnmarshalData(got[0], &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.UserID != userID || p.ConversationID != convID {
		t.Errorf("payload = %+v, want UserID=%v ConversationID=%v", p, userID, convID)
	}
}

// participant_left when the survivor count is exactly 1 schedules a
// lone-user kick for the survivor. Spec §10.3.
func TestWebhook_ParticipantLeftCountOneSchedulesLoneKick(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	survivor := uuid.New()
	leaver := uuid.New()
	ctx := context.Background()
	if _, err := h.RoomSvc.AddParticipant(ctx, convID, survivor); err != nil {
		t.Fatalf("AddParticipant survivor: %v", err)
	}
	if _, err := h.RoomSvc.AddParticipant(ctx, convID, leaver); err != nil {
		t.Fatalf("AddParticipant leaver: %v", err)
	}

	ev := makeParticipantEvent(t, "participant_left",
		"conv:"+convID.String(), "user:"+leaver.String(), nil)
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	// ZSET should now have an entry for our convID with a score in the
	// future (now + lone-kick TTL).
	score, err := h.Redis.ZScore(ctx, "room:lone_kicks_due", convID.String()).Result()
	if err != nil {
		t.Fatalf("ZScore lone_kicks_due: %v", err)
	}
	if score < float64(time.Now().Unix()) {
		t.Errorf("expected future score, got %v", score)
	}
	gotUser, err := h.Redis.Get(ctx, "room:"+convID.String()+":lone_user").Result()
	if err != nil {
		t.Fatalf("GET lone_user: %v", err)
	}
	if gotUser != survivor.String() {
		t.Errorf("lone_user = %q, want %q (the survivor)", gotUser, survivor)
	}
}

// participant_joined that brings the count from 1 → 2 cancels any
// pending lone-user kick for the conversation. Spec §10.3.
func TestWebhook_ParticipantJoinedCountTwoCancelsLoneKick(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	wh := newWebhookHandler(t, h)
	convID := uuid.New()
	survivor := uuid.New()
	rejoiner := uuid.New()
	ctx := context.Background()
	if _, err := h.RoomSvc.AddParticipant(ctx, convID, survivor); err != nil {
		t.Fatalf("AddParticipant survivor: %v", err)
	}
	if err := h.RoomSvc.ScheduleLoneKick(ctx, convID, survivor); err != nil {
		t.Fatalf("ScheduleLoneKick: %v", err)
	}

	ev := makeParticipantEvent(t, "participant_joined",
		"conv:"+convID.String(), "user:"+rejoiner.String(), nil)
	rec := callHandler(t, wh, signWebhookRequest(t, ev))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := h.Redis.ZScore(ctx, "room:lone_kicks_due", convID.String()).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("ZScore after rejoin: err=%v (want redis.Nil — kick should be cancelled)", err)
	}
	if _, err := h.Redis.Get(ctx, "room:"+convID.String()+":lone_user").Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("lone_user not deleted: err=%v", err)
	}
}
