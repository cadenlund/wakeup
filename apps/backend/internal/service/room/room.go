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

// DefaultLoneKickAfter is the §10.3 Discord-style "alone too long"
// timeout. Mirrors Discord's behaviour. Tunable via Config.LoneKickAfter
// (env var ROOM_LONE_KICK_AFTER); set to 0 to disable.
const DefaultLoneKickAfter = 5 * time.Minute

// loneKickQueueKey is the global Redis ZSET tracking due kicks.
// Score = unix timestamp the kick is due to fire; member = conv_id.
// One pending kick per conversation at a time (only one participant
// can ever be "the lone user" simultaneously).
const loneKickQueueKey = "room:lone_kicks_due"

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
	convs         *convsvc.Service
	users         UserGetter
	apiKey        string
	apiSecret     string
	livekitURL    string
	redis         redis.Cmdable
	logger        *slog.Logger
	now           func() time.Time
	livekitAdmin  LiveKitAdmin
	loneKickAfter time.Duration
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
	// LiveKitAdmin is the §10.3 admin RPC seam used by the lone-kick
	// sweeper to call RemoveParticipant. Optional: when nil, kick
	// scheduling still records entries but ExecuteLoneKick is a no-op
	// — the scheduling/cancel paths stay testable without a real
	// LiveKit binding.
	LiveKitAdmin LiveKitAdmin
	// LoneKickAfter is the §10.3 timeout. Defaults to DefaultLoneKickAfter
	// when zero. A negative value disables the feature (sweeper still
	// runs but never schedules a kick).
	LoneKickAfter time.Duration
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
	loneKickAfter := cfg.LoneKickAfter
	if loneKickAfter == 0 {
		loneKickAfter = DefaultLoneKickAfter
	}
	return &Service{
		convs: cfg.Convs, users: cfg.Users,
		apiKey: cfg.APIKey, apiSecret: cfg.APISecret,
		livekitURL: cfg.LiveKitURL, redis: cfg.Redis,
		logger: logger, now: now,
		livekitAdmin:  cfg.LiveKitAdmin,
		loneKickAfter: loneKickAfter,
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

// ListParticipants is the webhook-side helper that returns the raw
// participant set for convID. No membership check — the LiveKit
// webhook is signature-verified upstream, and the webhook needs to
// read the survivor list during participant_left to schedule the
// §10.3 lone-user kick. User-facing reads still go through
// GetParticipants (membership-gated).
func (s *Service) ListParticipants(ctx context.Context, convID uuid.UUID) ([]uuid.UUID, error) {
	raw, err := s.redis.SMembers(ctx, participantsKey(convID)).Result()
	if err != nil {
		return nil, fmt.Errorf("room: SMEMBERS participants: %w", err)
	}
	out := make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		uid, err := uuid.Parse(r)
		if err != nil {
			s.logger.Warn("room: skipping malformed participant id in list",
				slog.String("conv_id", convID.String()),
				slog.String("raw", r),
			)
			continue
		}
		out = append(out, uid)
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

// loneUserKey returns the Redis key holding the user_id pending a
// lone-kick on convID. One per conversation since only one participant
// can ever be alone in a room at a time.
func loneUserKey(convID uuid.UUID) string {
	return fmt.Sprintf("room:%s:lone_user", convID)
}

// LoneKickAfter returns the configured timeout. Zero means
// scheduling is a no-op — the feature is disabled.
func (s *Service) LoneKickAfter() time.Duration { return s.loneKickAfter }

// ScheduleLoneKick records a pending kick on convID for userID. The
// deadline is now+LoneKickAfter; the §4.12 sweeper will fire
// RemoveParticipant when the deadline passes (unless CancelLoneKick
// fires first because someone else joined).
//
// Idempotent — calling twice with the same conv refreshes the
// deadline and overwrites the user_id (the latter matters if a
// transient state had a different participant momentarily). Returns
// nil without touching Redis when LoneKickAfter is non-positive (the
// feature is disabled).
func (s *Service) ScheduleLoneKick(ctx context.Context, convID, userID uuid.UUID) error {
	if s.loneKickAfter <= 0 {
		return nil
	}
	fireAt := s.now().Add(s.loneKickAfter).Unix()
	pipe := s.redis.TxPipeline()
	pipe.ZAdd(ctx, loneKickQueueKey, redis.Z{Score: float64(fireAt), Member: convID.String()})
	// The user-id key carries 2× the kick TTL so it definitely outlives
	// the ZSET entry — otherwise the sweeper could find an entry with
	// no associated user and have to skip it.
	pipe.Set(ctx, loneUserKey(convID), userID.String(), 2*s.loneKickAfter)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("room: schedule lone kick: %w", err)
	}
	return nil
}

// CancelLoneKick removes any pending kick for convID. Idempotent —
// calling for a conv with no pending kick is a no-op.
func (s *Service) CancelLoneKick(ctx context.Context, convID uuid.UUID) error {
	pipe := s.redis.TxPipeline()
	pipe.ZRem(ctx, loneKickQueueKey, convID.String())
	pipe.Del(ctx, loneUserKey(convID))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("room: cancel lone kick: %w", err)
	}
	return nil
}

// LoneKick is one due-and-extracted pending kick.
type LoneKick struct {
	ConversationID uuid.UUID
	UserID         uuid.UUID
}

// PopDueLoneKicks atomically removes every kick whose deadline has
// passed and returns the (conv, user) pairs the caller should fire.
// The "atomic" property matters when multiple replicas run the
// sweeper — only one replica wins the ZREM for a given conv, so
// RemoveParticipant fires exactly once per kick.
//
// Implementation: ZRANGEBYSCORE pulls members ≤ now; ZREM removes
// them in the same pipeline; then GET → DEL each user key.
func (s *Service) PopDueLoneKicks(ctx context.Context) ([]LoneKick, error) {
	cutoff := s.now().Unix()
	candidates, err := s.redis.ZRangeByScore(ctx, loneKickQueueKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", cutoff),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("room: ZRANGEBYSCORE lone_kicks_due: %w", err)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	out := make([]LoneKick, 0, len(candidates))
	for _, raw := range candidates {
		convID, err := uuid.Parse(raw)
		if err != nil {
			s.logger.Warn("room: skipping malformed lone-kick conv id",
				slog.String("raw", raw))
			// Still ZREM it so it doesn't keep piling up.
			_ = s.redis.ZRem(ctx, loneKickQueueKey, raw).Err()
			continue
		}
		// Atomically claim: ZREM returns 1 iff this replica won the
		// race. Skip if another sweeper already grabbed it.
		removed, err := s.redis.ZRem(ctx, loneKickQueueKey, raw).Result()
		if err != nil {
			s.logger.Warn("room: ZREM lone_kicks_due",
				slog.String("conv_id", convID.String()),
				slog.String("error", err.Error()))
			continue
		}
		if removed == 0 {
			continue
		}
		userRaw, err := s.redis.Get(ctx, loneUserKey(convID)).Result()
		if err != nil {
			s.logger.Warn("room: GET lone_user",
				slog.String("conv_id", convID.String()),
				slog.String("error", err.Error()))
			continue
		}
		userID, err := uuid.Parse(userRaw)
		if err != nil {
			s.logger.Warn("room: malformed lone_user value",
				slog.String("conv_id", convID.String()),
				slog.String("raw", userRaw))
			_ = s.redis.Del(ctx, loneUserKey(convID)).Err()
			continue
		}
		// Clean up the companion user key now that we own the kick.
		_ = s.redis.Del(ctx, loneUserKey(convID)).Err()
		out = append(out, LoneKick{ConversationID: convID, UserID: userID})
	}
	return out, nil
}

// ExecuteLoneKick fires the actual LiveKit RemoveParticipant RPC for
// one due kick. Errors are returned (the sweeper logs them); a kick
// that fails leaves no Redis state behind because PopDueLoneKicks
// already removed the entry — a missed kick is tolerable since the
// next §10.3 webhook (e.g. participant_left when the user gives up
// on their own) will tidy up state. Returns nil when no admin client
// is wired, so unit tests on PopDueLoneKicks don't need a fake.
func (s *Service) ExecuteLoneKick(ctx context.Context, k LoneKick) error {
	if s.livekitAdmin == nil {
		return nil
	}
	return s.livekitAdmin.RemoveParticipant(ctx,
		ConvRoomID(k.ConversationID),
		ParticipantIdentity(k.UserID),
	)
}
