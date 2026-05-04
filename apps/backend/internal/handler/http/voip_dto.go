package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// RegisterVoIPTokenRequest is the body of POST /v1/devices/voip.
// PushKit tokens are iOS-only by definition; no `platform` field.
type RegisterVoIPTokenRequest struct {
	VoIPToken string `json:"voip_token" validate:"required" example:"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"`
}

// VoIPTokenResponse is the wire shape for one voip_tokens row.
type VoIPTokenResponse struct {
	ID         uuid.UUID `json:"id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	VoIPToken  string    `json:"voip_token"   example:"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"`
	CreatedAt  time.Time `json:"created_at"   example:"2026-05-02T10:42:55.412Z"`
	LastSeenAt time.Time `json:"last_seen_at" example:"2026-05-02T10:42:55.412Z"`
}

func toVoIPTokenResponse(t domain.VoIPToken) VoIPTokenResponse {
	return VoIPTokenResponse{
		ID:         t.ID,
		VoIPToken:  t.VoIPToken,
		CreatedAt:  t.CreatedAt,
		LastSeenAt: t.LastSeenAt,
	}
}
