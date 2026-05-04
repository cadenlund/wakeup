package room

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// LoneKickSweeperInterval is the §4.12 tick rate for the kick sweeper.
// 30s mirrors the presence sweeper. The user-visible delay between
// "lone-kick deadline passes" and "actual disconnect" is bounded by
// this — picking a longer interval would just stretch that out.
const LoneKickSweeperInterval = 30 * time.Second

// LoneKickSweeper implements job.Job for the §10.3 lone-user kick.
// On each tick it asks the room service for due kicks, then fires
// LiveKit's RemoveParticipant admin RPC for each. Errors per-kick
// are logged at warn level — a transient LiveKit blip on one kick
// doesn't kill the sweeper.
type LoneKickSweeper struct {
	Svc      *Service
	Logger   *slog.Logger
	interval time.Duration
}

// NewLoneKickSweeper validates inputs and returns the sweeper.
// Returns an error when Svc is nil; logger / interval default to
// slog.Default() and LoneKickSweeperInterval respectively.
func NewLoneKickSweeper(svc *Service, logger *slog.Logger, interval time.Duration) (*LoneKickSweeper, error) {
	if svc == nil {
		return nil, errors.New("room: NewLoneKickSweeper: Svc is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = LoneKickSweeperInterval
	}
	return &LoneKickSweeper{Svc: svc, Logger: logger, interval: interval}, nil
}

// Name implements job.Job.
func (*LoneKickSweeper) Name() string { return "lone-kick-sweeper" }

// Interval implements job.Job.
func (s *LoneKickSweeper) Interval() time.Duration { return s.interval }

// Run implements job.Job. Pops every due kick atomically, fires
// each in turn, logs failures. Returns nil even if individual kicks
// fail — the runner only treats the return as a "tick failed"
// signal, and we want partial successes to count.
func (s *LoneKickSweeper) Run(ctx context.Context) error {
	due, err := s.Svc.PopDueLoneKicks(ctx)
	if err != nil {
		return err
	}
	for _, k := range due {
		if err := s.Svc.ExecuteLoneKick(ctx, k); err != nil {
			s.Logger.Warn("room: lone kick fire",
				slog.String("conv_id", k.ConversationID.String()),
				slog.String("user_id", k.UserID.String()),
				slog.String("error", err.Error()),
			)
			continue
		}
		s.Logger.Info("room: lone-user kicked after timeout",
			slog.String("conv_id", k.ConversationID.String()),
			slog.String("user_id", k.UserID.String()),
		)
	}
	return nil
}
