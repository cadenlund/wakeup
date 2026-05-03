// Package room is the §10 voice/video service. Issues LiveKit JWTs
// scoped to a conversation, authorizes joins via the conversation
// member check, and reads the per-room participant set Redis (the
// LiveKit webhook handler in 10.4 writes to that set on
// participant_joined/left events).
//
// Token shape (§10.3 / §12.8.1):
//
//   - room: `conv:<conversation_id>`
//   - identity: `user:<user_id>`           ← stable so participant_joined webhooks map back
//   - roomJoin: true
//   - canPublish: true / canSubscribe: true / canPublishData: true
//   - canPublishSources: ["microphone", "camera"]
//   - metadata: {display_name, avatar_url, video}
//   - exp: now + 10 min   (LiveKit auto-refreshes during the connection)
package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/auth"
	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
)

// TokenTTL is the §10.3 / §12.8.1 token lifetime.
const TokenTTL = 10 * time.Minute

// participantSetTTL is the safety net per §10.3 — Redis keys expire
// after 24h to avoid stuck state if a webhook is dropped.
const participantSetTTL = 24 * time.Hour

// ConvRoomID returns the LiveKit room name for a conversation per
// §10.3 (room_id == conversation_id, prefixed for clarity).
func ConvRoomID(convID uuid.UUID) string { return "conv:" + convID.String() }

// ParticipantIdentity returns the LiveKit identity for a user per
// §10.3 (`user:<user_id>` so participant_joined webhooks map back).
func ParticipantIdentity(userID uuid.UUID) string { return "user:" + userID.String() }

// UserGetter is the slice of the user service the room service
// needs: profile lookup for the JWT metadata. Local interface so
// tests can stub it without spinning up the user service.
type UserGetter interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// Service is the room service.
type Service struct {
	convs      *convsvc.Service
	users      UserGetter
	apiKey     string
	apiSecret  string
	livekitURL string
	redis      redis.Cmdable
	logger     *slog.Logger
	now        func() time.Time
}

// Config builds the service.
type Config struct {
	Convs      *convsvc.Service
	Users      UserGetter
	APIKey     string
	APISecret  string
	LiveKitURL string
	Redis      redis.Cmdable
	Logger     *slog.Logger
	// Now lets tests inject a fake clock for the §12.8.1 token TTL
	// assertion. Defaults to time.Now.
	Now func() time.Time
}

// New constructs the service.
func New(cfg Config) (*Service, error) {
	if cfg.Convs == nil {
		return nil, errors.New("room: Config.Convs is required")
	}
	if cfg.Users == nil {
		return nil, errors.New("room: Config.Users is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("room: Config.APIKey is required")
	}
	if strings.TrimSpace(cfg.APISecret) == "" {
		return nil, errors.New("room: Config.APISecret is required")
	}
	if strings.TrimSpace(cfg.LiveKitURL) == "" {
		return nil, errors.New("room: Config.LiveKitURL is required")
	}
	if cfg.Redis == nil {
		return nil, errors.New("room: Config.Redis is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		convs: cfg.Convs, users: cfg.Users,
		apiKey: cfg.APIKey, apiSecret: cfg.APISecret,
		livekitURL: cfg.LiveKitURL, redis: cfg.Redis,
		logger: logger, now: now,
	}, nil
}

// JoinResult is what the handler returns on a successful Join.
type JoinResult struct {
	RoomID       string    `json:"room_id"`
	LiveKitURL   string    `json:"livekit_url"`
	LiveKitToken string    `json:"livekit_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Video        bool      `json:"video"`
}

// Participant is one entry in GetParticipants.
type Participant struct {
	UserID   uuid.UUID `json:"user_id"`
	JoinedAt time.Time `json:"joined_at"`
	Video    bool      `json:"video"`
}

// State is what GetParticipants returns.
type State struct {
	Participants []Participant `json:"participants"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
}

// MetadataPayload is the JSON shape stamped into the LiveKit JWT's
// metadata claim. LiveKit relays this verbatim to other participants
// so they can render display_name + avatar_url without a second
// backend call.
type MetadataPayload struct {
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
	Video       bool    `json:"video"`
}

// Join authorizes the caller, builds a LiveKit JWT, and returns the
// connection details. video is a UI hint baked into the token's
// metadata — it does NOT change the publish permissions (per §10.3
// the camera is always grant-allowed; the flag just tells the
// frontend whether this user intends to publish video on connect).
func (s *Service) Join(ctx context.Context, userID, convID uuid.UUID, video bool) (JoinResult, error) {
	if _, err := s.convs.Get(ctx, userID, convID); err != nil {
		return JoinResult{}, err
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return JoinResult{}, apierror.Internal("room: get user").WithCause(err)
	}

	metadata := MetadataPayload{
		DisplayName: user.DisplayName,
		AvatarURL:   user.AvatarURL,
		Video:       video,
	}
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return JoinResult{}, apierror.Internal("room: marshal metadata").WithCause(err)
	}

	canTrue := true
	at := auth.NewAccessToken(s.apiKey, s.apiSecret).
		SetIdentity(ParticipantIdentity(userID)).
		SetMetadata(string(metaBytes)).
		SetValidFor(TokenTTL).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin:          true,
			Room:              ConvRoomID(convID),
			CanPublish:        &canTrue,
			CanSubscribe:      &canTrue,
			CanPublishData:    &canTrue,
			CanPublishSources: []string{"microphone", "camera"},
		})
	tok, err := at.ToJWT()
	if err != nil {
		return JoinResult{}, apierror.Internal("room: sign jwt").WithCause(err)
	}

	return JoinResult{
		RoomID:       ConvRoomID(convID),
		LiveKitURL:   s.livekitURL,
		LiveKitToken: tok,
		ExpiresAt:    s.now().Add(TokenTTL),
		Video:        video,
	}, nil
}

// Leave is best-effort cleanup — the LiveKit `participant_left`
// webhook is the source of truth, so this method only confirms the
// caller is still a member of the conversation (so non-members can't
// poke at room state) and returns. Future work could SREM the
// caller's user_id from the participant set, but doing so before the
// LiveKit webhook fires would race the actual disconnect.
func (s *Service) Leave(ctx context.Context, userID, convID uuid.UUID) error {
	if _, err := s.convs.Get(ctx, userID, convID); err != nil {
		return err
	}
	return nil
}

// participantsKey returns the Redis SET key for a conversation's
// active participants per §10.3.
func participantsKey(convID uuid.UUID) string {
	return fmt.Sprintf("room:%s:participants", convID)
}

// startedAtKey returns the Redis key for a conversation's
// room.started_at timestamp per §10.3.
func startedAtKey(convID uuid.UUID) string {
	return fmt.Sprintf("room:%s:started_at", convID)
}

// participantVideoKey returns the Redis key for a per-participant
// video flag per §10.3.
func participantVideoKey(convID, userID uuid.UUID) string {
	return fmt.Sprintf("room:%s:participant:%s:video", convID, userID)
}

// participantJoinedAtKey returns the Redis key for the per-participant
// joined-at timestamp surfaced by §6.2 GetParticipants. Stamped on
// AddParticipant; cleared on RemoveParticipant alongside the video
// flag. (CodeRabbit PR #56.)
func participantJoinedAtKey(convID, userID uuid.UUID) string {
	return fmt.Sprintf("room:%s:participant:%s:joined_at", convID, userID)
}

// GetParticipants returns the current participant list + started_at
// for the conversation's room. Membership-gated. Reads from Redis;
// the webhook handler in 10.4 keeps the set fresh.
func (s *Service) GetParticipants(ctx context.Context, userID, convID uuid.UUID) (State, error) {
	if _, err := s.convs.Get(ctx, userID, convID); err != nil {
		return State{}, err
	}
	ids, err := s.redis.SMembers(ctx, participantsKey(convID)).Result()
	if err != nil {
		return State{}, apierror.Internal("room: SMEMBERS").WithCause(err)
	}
	out := State{Participants: make([]Participant, 0, len(ids))}
	for _, raw := range ids {
		uid, err := uuid.Parse(raw)
		if err != nil {
			s.logger.Warn("room: skipping malformed participant id",
				slog.String("conv_id", convID.String()),
				slog.String("raw", raw),
			)
			continue
		}
		video, _ := s.redis.Get(ctx, participantVideoKey(convID, uid)).Result()
		joined := time.Time{}
		if rawTS, err := s.redis.Get(ctx, participantJoinedAtKey(convID, uid)).Result(); err == nil {
			if t, err := time.Parse(time.RFC3339Nano, rawTS); err == nil {
				joined = t
			}
		}
		out.Participants = append(out.Participants, Participant{
			UserID:   uid,
			JoinedAt: joined,
			Video:    video == "true",
		})
	}
	startedRaw, err := s.redis.Get(ctx, startedAtKey(convID)).Result()
	if err == nil {
		if t, err := time.Parse(time.RFC3339Nano, startedRaw); err == nil {
			out.StartedAt = &t
		}
	}
	return out, nil
}

// AddParticipant is the webhook-side helper — called by the §10.4
// LiveKit webhook handler on `participant_joined`. SADD's the user
// to the participant set, stamps the joined-at timestamp (only on
// the first add — at-least-once delivery means we may see the same
// event again, and we want the original join time), and refreshes
// the room TTL.
//
// Returns true iff the SADD created a new entry (so the caller can
// short-circuit duplicate WS broadcasts on at-least-once webhook
// delivery).
func (s *Service) AddParticipant(ctx context.Context, convID, userID uuid.UUID) (bool, error) {
	added, err := s.redis.SAdd(ctx, participantsKey(convID), userID.String()).Result()
	if err != nil {
		return false, fmt.Errorf("room: SADD: %w", err)
	}
	// Refresh TTL on every join so a long-lived room doesn't expire
	// out from under us.
	if err := s.redis.Expire(ctx, participantsKey(convID), participantSetTTL).Err(); err != nil {
		return false, fmt.Errorf("room: EXPIRE: %w", err)
	}
	if added > 0 {
		// First add — stamp joined_at. SETNX so a duplicate event
		// (at-least-once) on a participant who briefly left and
		// rejoined doesn't overwrite a prior session's clock.
		if err := s.redis.Set(ctx, participantJoinedAtKey(convID, userID),
			s.now().Format(time.RFC3339Nano), participantSetTTL).Err(); err != nil {
			return false, fmt.Errorf("room: SET joined_at: %w", err)
		}
	}
	return added > 0, nil
}

// RemoveParticipant is the webhook-side helper — called by §10.4 on
// `participant_left`. SREMs the user from the participant set.
// Returns the new set size so the caller can decide whether to fire
// `room.ended`.
func (s *Service) RemoveParticipant(ctx context.Context, convID, userID uuid.UUID) (int64, error) {
	if _, err := s.redis.SRem(ctx, participantsKey(convID), userID.String()).Result(); err != nil {
		return 0, fmt.Errorf("room: SREM: %w", err)
	}
	if err := s.redis.Del(ctx, participantVideoKey(convID, userID)).Err(); err != nil {
		return 0, fmt.Errorf("room: DEL video: %w", err)
	}
	if err := s.redis.Del(ctx, participantJoinedAtKey(convID, userID)).Err(); err != nil {
		return 0, fmt.Errorf("room: DEL joined_at: %w", err)
	}
	size, err := s.redis.SCard(ctx, participantsKey(convID)).Result()
	if err != nil {
		return 0, fmt.Errorf("room: SCARD: %w", err)
	}
	if size == 0 {
		// Last participant left — drop the room metadata.
		_ = s.redis.Del(ctx, startedAtKey(convID)).Err()
	}
	return size, nil
}

// MarkStarted stamps room.started_at with the current time iff the
// key isn't already set. Returns true if this call was the writer
// (so the §10.4 webhook handler knows to fire room.started exactly
// once even on at-least-once delivery).
func (s *Service) MarkStarted(ctx context.Context, convID uuid.UUID) (bool, error) {
	ok, err := s.redis.SetNX(ctx, startedAtKey(convID),
		s.now().Format(time.RFC3339Nano), participantSetTTL).Result()
	if err != nil {
		return false, fmt.Errorf("room: SETNX started_at: %w", err)
	}
	return ok, nil
}

// SetParticipantVideo stamps the per-participant video flag. Used by
// the webhook handler on track_published / track_unpublished events.
func (s *Service) SetParticipantVideo(ctx context.Context, convID, userID uuid.UUID, video bool) error {
	val := "false"
	if video {
		val = "true"
	}
	if err := s.redis.Set(ctx, participantVideoKey(convID, userID), val, participantSetTTL).Err(); err != nil {
		return fmt.Errorf("room: SET video: %w", err)
	}
	return nil
}
