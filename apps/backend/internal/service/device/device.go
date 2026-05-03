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

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	repo "github.com/cadenlund/wakeup/apps/backend/internal/repository/devicetoken"
)

// Service is the device-token write surface used by the HTTP handlers.
type Service struct {
	devices *repo.Queries
}

// Config builds the service.
type Config struct {
	Devices *repo.Queries
}

// New constructs the service.
func New(cfg Config) (*Service, error) {
	if cfg.Devices == nil {
		return nil, errors.New("device: Config.Devices is required")
	}
	return &Service{devices: cfg.Devices}, nil
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
	if expoToken == "" {
		return domain.DeviceToken{}, apierror.BadRequest("expo_token is required")
	}
	tok, err := s.devices.Register(ctx, userID, expoToken, platform)
	if err != nil {
		return domain.DeviceToken{}, apierror.Internal("register device token").WithCause(err)
	}
	return tok, nil
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
