// Package notification is the §11 service that turns "user X is offline,
// send them a push" into a single call site. Composes:
//
//   - notificationpref.Service.ShouldNotify  — gate by per-category toggle
//   - devicetoken.Queries.ListByUser         — fetch the user's Expo tokens
//   - pushnotif.Pusher.Send                  — actually deliver via Expo
//
// Trigger sites in §16 milestone 11.5 (MessageService.Send,
// FriendService.SendRequest, room/call dispatch) wire this in once they
// detect "no live WS connection" — it shouldn't be called for users who
// already received the WS event.
package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pushnotif"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
)

// Category aliases notificationpref.Category so callers don't need to
// import two packages just to name a category.
type Category = notificationpref.Category

// PrefChecker is the slice of notificationpref.Service this package needs.
// Defining it here lets tests stub the gate without spinning up a real
// preferences DB.
type PrefChecker interface {
	ShouldNotify(ctx context.Context, userID uuid.UUID, category notificationpref.Category) bool
}

// DeviceTokenLister is the slice of devicetoken.Queries this package needs.
type DeviceTokenLister interface {
	ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.DeviceToken, error)
}

// SuppressionChecker gates push delivery on the §10.2 sticky DND intent
// and the per-conversation mute_until column. A non-nil convID is the
// signal "this push is conversation-scoped" — for those, both DND and
// per-member mute apply. For non-conversation-scoped pushes (friend
// requests), pass nil convID; only DND applies.
//
// The package ships nopSuppression for tests / call sites that don't
// want gating; production wires the adapter over presence_states +
// conversation_members.
type SuppressionChecker interface {
	PushSuppressed(ctx context.Context, userID uuid.UUID, convID *uuid.UUID) (bool, error)
}

// nopSuppression always returns false. Default when Config.Suppression
// is nil — keeps tests that don't care about suppression simple.
type nopSuppression struct{}

func (nopSuppression) PushSuppressed(context.Context, uuid.UUID, *uuid.UUID) (bool, error) {
	return false, nil
}

// Service fans an offline-push request out to Expo.
type Service struct {
	prefs       PrefChecker
	devices     DeviceTokenLister
	pusher      pushnotif.Pusher
	suppression SuppressionChecker
}

// Config builds the service.
type Config struct {
	Prefs   PrefChecker
	Devices DeviceTokenLister
	Pusher  pushnotif.Pusher
	// Suppression is optional. Nil = nopSuppression (always deliver).
	// Production wires an adapter that reads presence_states +
	// conversation_members; tests typically pass nil and exercise the
	// suppression layer in its own unit tests.
	Suppression SuppressionChecker
}

// New constructs the service. Returns an error if any dependency is missing.
func New(cfg Config) (*Service, error) {
	if cfg.Prefs == nil {
		return nil, errors.New("notification: Config.Prefs is required")
	}
	if cfg.Devices == nil {
		return nil, errors.New("notification: Config.Devices is required")
	}
	if cfg.Pusher == nil {
		return nil, errors.New("notification: Config.Pusher is required")
	}
	suppression := cfg.Suppression
	if suppression == nil {
		suppression = nopSuppression{}
	}
	return &Service{
		prefs: cfg.Prefs, devices: cfg.Devices, pusher: cfg.Pusher,
		suppression: suppression,
	}, nil
}

// SendOfflinePush gates push delivery and fans out to Expo. Three gates
// in order, each a silent no-op when it trips:
//  1. The recipient's per-category preference (§11.5).
//  2. Sticky DND intent on presence_states (§10.2). DND survives WS
//     disconnect; pushes are suppressed but in-app surfaces still fire.
//  3. Per-conversation mute_until > now() — only when convID != nil.
//     Friend-request pushes pass nil convID and skip this gate.
//
// Errors from gate 2/3 are returned to the caller; per §11 trigger
// sites treat push as best-effort and shouldn't block their main flow
// on a push failure.
func (s *Service) SendOfflinePush(ctx context.Context, recipientID uuid.UUID, category Category, payload pushnotif.Notification, convID *uuid.UUID) error {
	if !s.prefs.ShouldNotify(ctx, recipientID, category) {
		return nil
	}
	suppressed, err := s.suppression.PushSuppressed(ctx, recipientID, convID)
	if err != nil {
		return fmt.Errorf("notification: suppression check: %w", err)
	}
	if suppressed {
		return nil
	}
	tokens, err := s.devices.ListByUser(ctx, recipientID)
	if err != nil {
		return fmt.Errorf("notification: list device tokens: %w", err)
	}
	if len(tokens) == 0 {
		return nil
	}
	expoTokens := make([]string, 0, len(tokens))
	for _, t := range tokens {
		expoTokens = append(expoTokens, t.ExpoToken)
	}
	if err := s.pusher.Send(ctx, expoTokens, payload); err != nil {
		slog.WarnContext(ctx, "notification: expo push failed",
			slog.String("recipient_id", recipientID.String()),
			slog.String("category", string(category)),
			slog.Int("token_count", len(expoTokens)),
			slog.Any("err", err),
		)
		return fmt.Errorf("notification: push send: %w", err)
	}
	return nil
}
