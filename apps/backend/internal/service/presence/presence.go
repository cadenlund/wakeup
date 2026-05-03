// Package presence is the §9 presence engine. Heartbeat / SetStatus
// flow through here, the §9.2 decay sweeper runs as a job.Job, and
// every state change publishes a §7.2 presence.update event to the
// caller's friends only (never to non-friends, never echoed back).
package presence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	presrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/presence"
	"github.com/cadenlund/wakeup/apps/backend/internal/wsproto"
)

// FriendLister is the slice of the friend service the presence service
// needs: enumerate every accepted friend's user_id for fan-out. Kept
// as a local interface so tests can stub it without spinning up the
// full friend service stack.
type FriendLister interface {
	ListAcceptedFriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
}

// Default decay cutoffs from §9.2.
const (
	DefaultOnlineCutoff  = 5 * time.Minute
	DefaultAwayCutoff    = time.Hour
	DefaultSweepInterval = 30 * time.Second
)

// Service is the presence engine.
type Service struct {
	repo    *presrepo.Queries
	broker  pubsub.Broker
	friends FriendLister
	logger  *slog.Logger

	onlineCutoff  time.Duration
	awayCutoff    time.Duration
	sweepInterval time.Duration
	now           func() time.Time
}

// Config builds the service.
type Config struct {
	Repo    *presrepo.Queries
	Broker  pubsub.Broker
	Friends FriendLister
	Logger  *slog.Logger

	// Cutoffs / interval default to the §9.2 values when zero.
	OnlineCutoff  time.Duration
	AwayCutoff    time.Duration
	SweepInterval time.Duration
	// Now lets tests inject a fake clock. Defaults to time.Now.
	Now func() time.Time
}

// New constructs the service. Negative duration fields are rejected
// (instead of silently defaulting) so a config typo surfaces at
// startup rather than as surprising prod behavior. (CodeRabbit PR #52
// — same pattern PR #45 settled on for the orphan sweeper.)
func New(cfg Config) (*Service, error) {
	if cfg.Repo == nil {
		return nil, errors.New("presence: Config.Repo is required")
	}
	if cfg.Friends == nil {
		return nil, errors.New("presence: Config.Friends is required")
	}
	if cfg.OnlineCutoff < 0 {
		return nil, fmt.Errorf("presence: Config.OnlineCutoff must be >= 0, got %v", cfg.OnlineCutoff)
	}
	if cfg.AwayCutoff < 0 {
		return nil, fmt.Errorf("presence: Config.AwayCutoff must be >= 0, got %v", cfg.AwayCutoff)
	}
	if cfg.SweepInterval < 0 {
		return nil, fmt.Errorf("presence: Config.SweepInterval must be >= 0, got %v", cfg.SweepInterval)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	online := cfg.OnlineCutoff
	if online == 0 {
		online = DefaultOnlineCutoff
	}
	away := cfg.AwayCutoff
	if away == 0 {
		away = DefaultAwayCutoff
	}
	sweep := cfg.SweepInterval
	if sweep == 0 {
		sweep = DefaultSweepInterval
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		repo: cfg.Repo, broker: cfg.Broker, friends: cfg.Friends,
		logger:       logger,
		onlineCutoff: online, awayCutoff: away, sweepInterval: sweep,
		now: now,
	}, nil
}

// Heartbeat is called by the WS handler when a client sends the §7.3
// heartbeat event. Refreshes timestamps and — when the row's status
// flips from `away` back to `online` — publishes presence.update.
func (s *Service) Heartbeat(ctx context.Context, userID uuid.UUID) error {
	prior, getErr := s.repo.Get(ctx, userID)
	if getErr != nil && !errors.Is(getErr, presrepo.ErrNotFound) {
		return apierror.Internal("presence: get prior").WithCause(getErr)
	}
	updated, err := s.repo.UpsertHeartbeat(ctx, userID)
	if err != nil {
		return apierror.Internal("presence: heartbeat").WithCause(err)
	}
	// Publish only on a real status change. Fresh rows (prior NotFound)
	// default priorStatus to offline so the offline→online transition
	// fires a fan-out. Use the GET error (not the upsert err, which
	// has just been re-assigned) — the prior version of this code
	// shadowed the variable and silently treated every fresh row's
	// prior as zero-value, losing the offline default. (CodeRabbit
	// PR #52.)
	priorStatus := domain.PresenceOffline
	if getErr == nil {
		priorStatus = prior.Status
	}
	if updated.Status != priorStatus {
		s.publish(ctx, updated)
	}
	return nil
}

// SetStatus is the §7.3 manual override. Validates the status,
// persists it, publishes presence.update on a change.
func (s *Service) SetStatus(ctx context.Context, userID uuid.UUID, status domain.PresenceStatus) error {
	if !status.IsValid() {
		return apierror.Validation([]apierror.FieldError{{
			Field: "status", Code: "INVALID_VALUE",
			Message: fmt.Sprintf("status %q is not one of online/away/offline/sleeping", status),
		}})
	}
	prior, err := s.repo.Get(ctx, userID)
	priorStatus := domain.PresenceOffline
	switch {
	case errors.Is(err, presrepo.ErrNotFound):
		// no prior row; falls through with priorStatus=offline
	case err != nil:
		return apierror.Internal("presence: get prior").WithCause(err)
	default:
		priorStatus = prior.Status
	}
	updated, err := s.repo.SetStatus(ctx, userID, status)
	if err != nil {
		return apierror.Internal("presence: set status").WithCause(err)
	}
	if updated.Status != priorStatus {
		s.publish(ctx, updated)
	}
	return nil
}

// Get returns the current presence row for userID. A user with no
// row yet is rendered as `offline` with timestamps from the call
// time — the §7.2 widget endpoint expects every requested user to
// resolve to a status string.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (domain.PresenceState, error) {
	row, err := s.repo.Get(ctx, userID)
	if errors.Is(err, presrepo.ErrNotFound) {
		now := s.now()
		return domain.PresenceState{
			UserID: userID, Status: domain.PresenceOffline,
			LastActiveAt: now, LastHeartbeatAt: now, UpdatedAt: now,
		}, nil
	}
	if err != nil {
		return domain.PresenceState{}, apierror.Internal("presence: get").WithCause(err)
	}
	return row, nil
}

// ListForUsers returns presence rows for the given user IDs. Missing
// users surface as offline rather than being silently dropped — the
// widget endpoint promises a row for every friend.
func (s *Service) ListForUsers(ctx context.Context, ids []uuid.UUID) ([]domain.PresenceState, error) {
	if len(ids) == 0 {
		return []domain.PresenceState{}, nil
	}
	rows, err := s.repo.ListByIDs(ctx, ids)
	if err != nil {
		return nil, apierror.Internal("presence: list by ids").WithCause(err)
	}
	byID := make(map[uuid.UUID]domain.PresenceState, len(rows))
	for _, r := range rows {
		byID[r.UserID] = r
	}
	out := make([]domain.PresenceState, 0, len(ids))
	now := s.now()
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			out = append(out, r)
			continue
		}
		out = append(out, domain.PresenceState{
			UserID: id, Status: domain.PresenceOffline,
			LastActiveAt: now, LastHeartbeatAt: now, UpdatedAt: now,
		})
	}
	return out, nil
}

// ListFriends returns the caller's friends' presence rendered with
// the same offline-fallback as ListForUsers. Used by the §6.1
// /v1/widget/friends + /v1/presence/friends endpoints.
func (s *Service) ListFriends(ctx context.Context, userID uuid.UUID) ([]domain.PresenceState, error) {
	friends, err := s.friends.ListAcceptedFriendIDs(ctx, userID)
	if err != nil {
		return nil, apierror.Internal("presence: list friend ids").WithCause(err)
	}
	return s.ListForUsers(ctx, friends)
}

// publishTimeout caps the per-publish work (friend lookup + every
// per-friend Publish). Bounded so a stuck pubsub backend doesn't pin
// a goroutine forever, but generous enough to absorb a slow Redis on
// a busy node.
const publishTimeout = 5 * time.Second

// publish sends a presence.update to every friend's user channel
// (§7.2: friends only, never echoed back to the source). Uses the
// FriendLister to enumerate friends — non-fatal if it fails (we
// already persisted the change; the user may be without WS at the
// moment).
//
// Detached context: the caller's ctx may be a request scope (HTTP or
// WS) that gets cancelled the moment the user's client disconnects.
// We've already committed the state change to Postgres at this point
// — the fan-out has to follow through regardless. Use a fresh
// background context with a bounded timeout so cancellation can't
// strand the publish. (CodeRabbit PR #52.)
func (s *Service) publish(_ context.Context, state domain.PresenceState) {
	if s.broker == nil {
		return
	}
	pubCtx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	friends, err := s.friends.ListAcceptedFriendIDs(pubCtx, state.UserID)
	if err != nil {
		s.logger.Warn("presence: list friends for fan-out failed",
			slog.String("user_id", state.UserID.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	if len(friends) == 0 {
		return
	}
	payload := wsproto.PresenceUpdatePayload{
		UserID: state.UserID, Status: string(state.Status),
		LastActiveAt: state.LastActiveAt,
	}
	encoded, err := wsproto.Encode(wsproto.EventPresenceUpdate, payload)
	if err != nil {
		s.logger.Warn("presence: encode event", slog.String("error", err.Error()))
		return
	}
	for _, friendID := range friends {
		channel := fmt.Sprintf("user:%s:events", friendID)
		if err := s.broker.Publish(pubCtx, channel, encoded); err != nil {
			s.logger.Warn("presence: publish",
				slog.String("channel", channel),
				slog.String("error", err.Error()),
			)
		}
	}
}

// --- §4.12 Job interface (decay sweeper) ----------------------------

// Name implements job.Job.
func (s *Service) Name() string { return "presence-decay-sweeper" }

// Interval implements job.Job. Defaults to 30s per §9.2.
func (s *Service) Interval() time.Duration { return s.sweepInterval }

// Run implements job.Job. Walks the §9.2 decay rules: online → away,
// away → offline. For each demoted row, publishes presence.update.
func (s *Service) Run(ctx context.Context) error {
	demoted, err := s.repo.DecayStale(ctx, s.onlineCutoff, s.awayCutoff)
	if err != nil {
		return fmt.Errorf("presence sweeper: %w", err)
	}
	for _, row := range demoted {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.publish(ctx, row)
	}
	if len(demoted) > 0 {
		s.logger.Info("presence sweeper: tick", slog.Int("demoted", len(demoted)))
	}
	return nil
}
