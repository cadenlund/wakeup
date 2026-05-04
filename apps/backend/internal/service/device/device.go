// Package device is the service layer for /v1/devices — Expo push token
// CRUD per §6.1 / §16 milestone 11.4. Wraps the devicetoken repository
// behind apierror-typed responses; the §11.3 notification service uses
// its own repo handle for the read path so this package only owns the
// writes (Register / Delete).
package device

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	repo "github.com/cadenlund/wakeup/apps/backend/internal/repository/devicetoken"
	voiprepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/voiptoken"
)

// Service is the device-token write surface used by the HTTP handlers.
// Wraps both the Expo-push token repo and the iOS PushKit (VoIP) token
// repo — same conceptual domain, separate tables.
type Service struct {
	devices *repo.Queries
	voip    *voiprepo.Queries
}

// Config builds the service.
type Config struct {
	Devices *repo.Queries
	// VoIP is optional. When nil, RegisterVoIP / ListVoIPForUser /
	// DeleteVoIP return apierror.NotFound — useful in tests that
	// don't care about the VoIP path.
	VoIP *voiprepo.Queries
}

// New constructs the service.
func New(cfg Config) (*Service, error) {
	if cfg.Devices == nil {
		return nil, errors.New("device: Config.Devices is required")
	}
	return &Service{devices: cfg.Devices, voip: cfg.VoIP}, nil
}

// Register stores or refreshes the user's Expo token. Idempotent on
// the (user_id, expo_token) UNIQUE pair: re-register bumps last_seen_at
// and refreshes the platform rather than creating a duplicate row.
//
// Validation (non-empty token, recognized platform) is the handler DTO's
// job via validator/v10 tags; the defensive checks here are belt-and-
// suspenders for callers that bypass HTTP (admin scripts, internal jobs).
func (s *Service) Register(ctx context.Context, userID uuid.UUID, expoToken string, platform domain.DevicePlatform) (domain.DeviceToken, error) {
	if !platform.IsValid() {
		return domain.DeviceToken{}, apierror.BadRequest(
			fmt.Sprintf("platform %q is not supported (must be ios or android)", platform),
		)
	}
	// Trim before persisting: migration 0009's CHECK constraint already
	// rejects all-whitespace tokens at the DB level, but failing here
	// surfaces a clean 400 instead of a 500.
	trimmed := strings.TrimSpace(expoToken)
	if trimmed == "" {
		return domain.DeviceToken{}, apierror.BadRequest("expo_token is required")
	}
	tok, err := s.devices.Register(ctx, userID, trimmed, platform)
	if err != nil {
		return domain.DeviceToken{}, apierror.Internal("register device token").WithCause(err)
	}
	return tok, nil
}

// RegisterVoIP stores or refreshes the user's iOS PushKit token for
// the §8.6 CallKit incoming-call ring. Idempotent on (user_id,
// voip_token) — re-register bumps last_seen_at instead of duplicating.
//
// VoIP tokens have no platform field — by definition iOS-only. Android
// uses a high-priority FCM data message via the existing Expo path.
func (s *Service) RegisterVoIP(ctx context.Context, userID uuid.UUID, voipToken string) (domain.VoIPToken, error) {
	if s.voip == nil {
		return domain.VoIPToken{}, apierror.NotFound("voip token storage")
	}
	trimmed := strings.TrimSpace(voipToken)
	if trimmed == "" {
		return domain.VoIPToken{}, apierror.BadRequest("voip_token is required")
	}
	tok, err := s.voip.Register(ctx, userID, trimmed)
	if err != nil {
		return domain.VoIPToken{}, apierror.Internal("register voip token").WithCause(err)
	}
	return tok, nil
}

// ListVoIPForUser returns every PushKit token registered to userID.
// Empty slice when none. Used alongside the existing /v1/devices list
// for the mobile settings/devices screen and (future) by the call
// fanout to enumerate VoIP recipients.
//
// Returns apierror.NotFound when VoIP storage isn't configured (s.voip
// is nil) — same posture as RegisterVoIP / DeleteVoIP, so a misconfigured
// deployment surfaces a typed error instead of silently looking like
// "no devices yet" (CodeRabbit on PR #105 — the prior nil/nil return
// drifted from the sibling methods).
func (s *Service) ListVoIPForUser(ctx context.Context, userID uuid.UUID) ([]domain.VoIPToken, error) {
	if s.voip == nil {
		return nil, apierror.NotFound("voip token storage")
	}
	tokens, err := s.voip.ListByUser(ctx, userID)
	if err != nil {
		return nil, apierror.Internal("list voip tokens").WithCause(err)
	}
	return tokens, nil
}

// DeleteVoIP removes the user's voip token by id. Same NotFound
// semantics as Delete (no enumeration leak across users).
func (s *Service) DeleteVoIP(ctx context.Context, userID, id uuid.UUID) error {
	if s.voip == nil {
		return apierror.NotFound("voip token")
	}
	if err := s.voip.Delete(ctx, id, userID); err != nil {
		if errors.Is(err, voiprepo.ErrNotFound) {
			return apierror.NotFound("voip token")
		}
		return apierror.Internal("delete voip token").WithCause(err)
	}
	return nil
}

// Delete removes the user's device token by id. Returns 404 NotFound if
// the row doesn't exist OR belongs to a different user (the repo's
// Delete is scoped to (id, userID), so the two cases are
// indistinguishable from out here — by design, no cross-user
// enumeration leak).
func (s *Service) Delete(ctx context.Context, userID, deviceID uuid.UUID) error {
	if err := s.devices.Delete(ctx, deviceID, userID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return apierror.NotFound("device token")
		}
		return apierror.Internal("delete device token").WithCause(err)
	}
	return nil
}
