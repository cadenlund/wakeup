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

// Service fans an offline-push request out to Expo.
type Service struct {
	prefs   PrefChecker
	devices DeviceTokenLister
	pusher  pushnotif.Pusher
}

// Config builds the service.
type Config struct {
	Prefs   PrefChecker
	Devices DeviceTokenLister
	Pusher  pushnotif.Pusher
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
	return &Service{prefs: cfg.Prefs, devices: cfg.Devices, pusher: cfg.Pusher}, nil
}

// SendOfflinePush gates by the user's per-category preference, then fans
// out the payload to every Expo token they've registered. A user with no
// devices (or with the category toggled off) is a silent no-op — that's
// the expected steady-state for a brand-new user with no mobile install.
//
// Errors are logged at warn-level and returned so call sites can decide
// whether to surface them; per §11 this is a best-effort side-channel,
// so trigger sites should not block their main flow on a push failure.
func (s *Service) SendOfflinePush(ctx context.Context, recipientID uuid.UUID, category Category, payload pushnotif.Notification) error {
	if !s.prefs.ShouldNotify(ctx, recipientID, category) {
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
