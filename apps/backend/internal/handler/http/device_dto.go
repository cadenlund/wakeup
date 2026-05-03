package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// RegisterDeviceRequest is the body of POST /v1/devices. The
// (user_id, expo_token) UNIQUE constraint in migration 0009 makes
// re-register an UPDATE-by-pair rather than a duplicate row, so the
// mobile client can call this freely on every login without churning rows.
type RegisterDeviceRequest struct {
	ExpoToken string `json:"expo_token" validate:"required" example:"ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"`
	Platform  string `json:"platform"   validate:"required,oneof=ios android" example:"ios"`
}

// DeviceTokenResponse is the wire shape for one device_tokens row.
type DeviceTokenResponse struct {
	ID         uuid.UUID `json:"id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	ExpoToken  string    `json:"expo_token"   example:"ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"`
	Platform   string    `json:"platform"     example:"ios"`
	CreatedAt  time.Time `json:"created_at"   example:"2026-05-02T10:42:55.412Z"`
	LastSeenAt time.Time `json:"last_seen_at" example:"2026-05-02T10:42:55.412Z"`
}

// toDeviceTokenResponse converts a domain.DeviceToken into the wire shape.
// The user_id is intentionally omitted — the caller already knows it (it's
// always the authenticated user) and the field is redundant on the wire.
func toDeviceTokenResponse(d domain.DeviceToken) DeviceTokenResponse {
	return DeviceTokenResponse{
		ID:         d.ID,
		ExpoToken:  d.ExpoToken,
		Platform:   string(d.Platform),
		CreatedAt:  d.CreatedAt,
		LastSeenAt: d.LastSeenAt,
	}
}
