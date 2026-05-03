package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	roomsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/room"
	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// LiveKit webhook event types we care about. Other events (egress,
// ingress) are received but ignored — we don't use those LiveKit
// features in v1.
const (
	livekitEventRoomStarted       = "room_started"
	livekitEventRoomFinished      = "room_finished"
	livekitEventParticipantJoined = "participant_joined"
	livekitEventParticipantLeft   = "participant_left"
	livekitEventTrackPublished    = "track_published"
	livekitEventTrackUnpublished  = "track_unpublished"
)

// LiveKitWebhookHandler is the §10.4 unauthenticated POST
// /webhooks/livekit endpoint. Signature-verified per §10.3 via the
// LiveKit-provided webhook.ReceiveWebhookEvent helper, which checks
// the HMAC Authorization header against the configured KeyProvider.
type LiveKitWebhookHandler struct {
	rooms       *roomsvc.Service
	broker      pubsub.Broker
	keyProvider auth.KeyProvider
	logger      *slog.Logger
}

// NewLiveKitWebhookHandler wires the handler.
func NewLiveKitWebhookHandler(
	rooms *roomsvc.Service,
	broker pubsub.Broker,
	keyProvider auth.KeyProvider,
	logger *slog.Logger,
) (*LiveKitWebhookHandler, error) {
	if rooms == nil {
		return nil, errors.New("httpapi: LiveKitWebhookHandler requires non-nil room service")
	}
	if broker == nil {
		return nil, errors.New("httpapi: LiveKitWebhookHandler requires non-nil broker")
	}
	if keyProvider == nil {
		return nil, errors.New("httpapi: LiveKitWebhookHandler requires non-nil keyProvider")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &LiveKitWebhookHandler{
		rooms: rooms, broker: broker,
		keyProvider: keyProvider, logger: logger,
	}, nil
}

// Handle is the /webhooks/livekit handler.
//
// @Summary      LiveKit webhook receiver
// @Description  Unauthenticated endpoint that receives LiveKit's room/participant lifecycle events. Signature verification happens inside the handler via the configured KeyProvider — there is no bearer-token / cookie auth here. Events that fail verification get 401; events for unknown rooms get 200 (no enumeration). Handled events: room_started, room_finished, participant_joined, participant_left, track_published (camera only), track_unpublished (camera only).
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Success      200  "ok"
// @Failure      401  {object} ErrorResponse  "Invalid or missing webhook signature"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /webhooks/livekit [post]
func (h *LiveKitWebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	event, err := webhook.ReceiveWebhookEvent(r, h.keyProvider)
	if err != nil {
		WriteError(w, r, apierror.Unauthorized("invalid livekit webhook signature").WithCause(err))
		return
	}
	if err := h.dispatch(r.Context(), event); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// dispatch routes one webhook event to the appropriate room-state
// + WS-broadcast actions. Unknown rooms (room name doesn't decode to
// a `conv:<uuid>` shape, or the conv doesn't exist) are silently
// dropped per §12.8.3 unknown_room (we don't want webhook traffic to
// confirm or deny the existence of a conversation).
func (h *LiveKitWebhookHandler) dispatch(ctx context.Context, event *livekit.WebhookEvent) error {
	if event == nil || event.Room == nil {
		return nil
	}
	convID, ok := parseConvRoomID(event.Room.Name)
	if !ok {
		// Some other LiveKit room (e.g. a future feature, or someone
		// else's room on a shared LiveKit). Ignore.
		return nil
	}
	switch event.Event {
	case livekitEventRoomStarted:
		return h.handleRoomStarted(ctx, convID)
	case livekitEventRoomFinished:
		return h.handleRoomFinished(ctx, convID)
	case livekitEventParticipantJoined:
		return h.handleParticipantJoined(ctx, convID, event.Participant)
	case livekitEventParticipantLeft:
		return h.handleParticipantLeft(ctx, convID, event.Participant)
	case livekitEventTrackPublished:
		return h.handleTrackChange(ctx, convID, event.Participant, event.Track, true)
	case livekitEventTrackUnpublished:
		return h.handleTrackChange(ctx, convID, event.Participant, event.Track, false)
	default:
		// egress_*, ingress_*, etc. — known, just unused. Silent drop.
		h.logger.Debug("livekit webhook: ignoring unhandled event",
			slog.String("event", event.Event),
		)
		return nil
	}
}

func (h *LiveKitWebhookHandler) handleRoomStarted(ctx context.Context, convID uuid.UUID) error {
	wasFirst, err := h.rooms.MarkStarted(ctx, convID)
	if err != nil {
		return apierror.Internal("livekit webhook: mark started").WithCause(err)
	}
	if !wasFirst {
		// At-least-once delivery: room already known to be started.
		return nil
	}
	h.publish(ctx, convID, wsproto.EventRoomStarted, map[string]any{
		"conversation_id": convID,
	})
	return nil
}

func (h *LiveKitWebhookHandler) handleRoomFinished(ctx context.Context, convID uuid.UUID) error {
	h.publish(ctx, convID, wsproto.EventRoomEnded, wsproto.RoomEndedPayload{
		ConversationID: convID,
	})
	return nil
}

func (h *LiveKitWebhookHandler) handleParticipantJoined(
	ctx context.Context, convID uuid.UUID, p *livekit.ParticipantInfo,
) error {
	if p == nil {
		return nil
	}
	userID, ok := parseUserIdentity(p.Identity)
	if !ok {
		// Identity didn't match `user:<uuid>` — could be a server-
		// side recorder or another non-user participant. Ignore.
		h.logger.Debug("livekit webhook: skipping non-user participant_joined",
			slog.String("identity", p.Identity),
		)
		return nil
	}
	added, err := h.rooms.AddParticipant(ctx, convID, userID)
	if err != nil {
		return apierror.Internal("livekit webhook: add participant").WithCause(err)
	}
	if !added {
		// Duplicate event under at-least-once delivery.
		return nil
	}
	h.publish(ctx, convID, wsproto.EventRoomParticipantJoined, wsproto.RoomParticipantJoinedPayload{
		ConversationID: convID,
		UserID:         userID,
		Video:          participantHasVideo(p),
	})
	return nil
}

func (h *LiveKitWebhookHandler) handleParticipantLeft(
	ctx context.Context, convID uuid.UUID, p *livekit.ParticipantInfo,
) error {
	if p == nil {
		return nil
	}
	userID, ok := parseUserIdentity(p.Identity)
	if !ok {
		return nil
	}
	size, err := h.rooms.RemoveParticipant(ctx, convID, userID)
	if err != nil {
		return apierror.Internal("livekit webhook: remove participant").WithCause(err)
	}
	h.publish(ctx, convID, wsproto.EventRoomParticipantLeft, wsproto.RoomParticipantLeftPayload{
		ConversationID: convID,
		UserID:         userID,
	})
	if size == 0 {
		h.publish(ctx, convID, wsproto.EventRoomEnded, wsproto.RoomEndedPayload{
			ConversationID: convID,
		})
	}
	return nil
}

func (h *LiveKitWebhookHandler) handleTrackChange(
	ctx context.Context, convID uuid.UUID, p *livekit.ParticipantInfo, track *livekit.TrackInfo, published bool,
) error {
	if p == nil || track == nil {
		return nil
	}
	if track.Source != livekit.TrackSource_CAMERA {
		// §12.8.3: only camera tracks fire room.video_changed.
		return nil
	}
	userID, ok := parseUserIdentity(p.Identity)
	if !ok {
		return nil
	}
	if err := h.rooms.SetParticipantVideo(ctx, convID, userID, published); err != nil {
		return apierror.Internal("livekit webhook: set video").WithCause(err)
	}
	h.publish(ctx, convID, wsproto.EventRoomVideoChanged, wsproto.RoomVideoChangedPayload{
		ConversationID: convID,
		UserID:         userID,
		Video:          published,
	})
	return nil
}

// publish encodes the event and writes it to the conv channel. The
// existing WS bridge (8.2) fans it out to subscribed users. Errors
// are logged but not surfaced — webhook handlers must always return
// 200 on a successfully-verified event so LiveKit doesn't retry
// indefinitely.
func (h *LiveKitWebhookHandler) publish(
	ctx context.Context, convID uuid.UUID, eventType wsproto.EventType, payload any,
) {
	encoded, err := wsproto.Encode(eventType, payload)
	if err != nil {
		h.logger.Warn("livekit webhook: encode event",
			slog.String("event", string(eventType)),
			slog.String("error", err.Error()),
		)
		return
	}
	channel := fmt.Sprintf("conv:%s:messages", convID)
	if err := h.broker.Publish(ctx, channel, encoded); err != nil {
		h.logger.Warn("livekit webhook: publish",
			slog.String("channel", channel),
			slog.String("error", err.Error()),
		)
	}
}

// parseConvRoomID inverts roomsvc.ConvRoomID — turns "conv:<uuid>"
// back into the conversation UUID. Returns ok=false on anything that
// doesn't fit the shape so we can silently drop unknown rooms.
func parseConvRoomID(name string) (uuid.UUID, bool) {
	if !strings.HasPrefix(name, "conv:") {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimPrefix(name, "conv:"))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// parseUserIdentity inverts roomsvc.ParticipantIdentity — turns
// "user:<uuid>" back into the user UUID. Returns ok=false on
// anything else (recorders / agents / SIP ingress, etc.).
func parseUserIdentity(identity string) (uuid.UUID, bool) {
	if !strings.HasPrefix(identity, "user:") {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimPrefix(identity, "user:"))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// participantHasVideo reports whether the participant currently has a
// camera track published. Used on participant_joined when the join
// already includes published tracks.
func participantHasVideo(p *livekit.ParticipantInfo) bool {
	if p == nil {
		return false
	}
	for _, tr := range p.Tracks {
		if tr.Source == livekit.TrackSource_CAMERA && !tr.Muted {
			return true
		}
	}
	return false
}
