package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// --- Public user views ---------------------------------------------------

// UserResponse is the public profile view used in lists, message senders,
// conversation members. NEVER includes email, password_hash, role, or
// notification preferences (§4.10).
type UserResponse struct {
	ID          uuid.UUID `json:"id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Username    string    `json:"username"     example:"caden"`
	DisplayName string    `json:"display_name" example:"Caden Lund"`
	AvatarURL   *string   `json:"avatar_url"   example:"https://wakeup.app/avatars/caden.png"`
	CreatedAt   time.Time `json:"created_at"   example:"2026-05-02T09:31:21.810Z"`
}

// MeResponse is the authenticated self view. Includes private fields the
// user is allowed to see about themselves (email, role, color_scheme).
// During admin impersonation (§8.7), this returns the IMPERSONATED user
// with ImpersonatedBy populated; that field is unused until milestone
// 12.x but defined now so the wire shape is locked from day one.
type MeResponse struct {
	ID             uuid.UUID         `json:"id"              example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Username       string            `json:"username"        example:"caden"`
	DisplayName    string            `json:"display_name"    example:"Caden Lund"`
	Email          string            `json:"email"           example:"caden@example.com"`
	AvatarURL      *string           `json:"avatar_url"      example:"https://wakeup.app/avatars/caden.png"`
	ColorScheme    string            `json:"color_scheme"    example:"system"`
	Role           string            `json:"role"            example:"user"`
	CreatedAt      time.Time         `json:"created_at"      example:"2026-05-02T09:31:21.810Z"`
	ImpersonatedBy *ImpersonatorInfo `json:"impersonated_by,omitempty"`
}

// ImpersonatorInfo identifies the admin currently impersonating this
// session so the UI can render a "you are impersonating @<user>" banner.
type ImpersonatorInfo struct {
	ID          uuid.UUID `json:"id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Username    string    `json:"username"     example:"baron"`
	DisplayName string    `json:"display_name" example:"Baron Admin"`
}

// toMeResponse renders the authenticated-self view. Soft-deleted is not
// expected here — callers must reject the session before reaching this
// converter — but we still strip the password_hash by omission (it isn't
// even a field on MeResponse, so JSON marshal can't leak it).
//
// For non-impersonation requests, callers pass nil for impersonator.
// During §8.7 admin impersonation, u is the IMPERSONATED user and
// impersonator is the admin owning the session — the resulting response
// renders the target's profile with `impersonated_by` populated.
func toMeResponse(u domain.User, impersonator *domain.User) MeResponse {
	resp := MeResponse{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
		Email: u.Email, AvatarURL: u.AvatarURL, ColorScheme: u.ColorScheme,
		Role: u.Role, CreatedAt: u.CreatedAt,
	}
	if impersonator != nil && impersonator.ID != u.ID {
		resp.ImpersonatedBy = &ImpersonatorInfo{
			ID:          impersonator.ID,
			Username:    impersonator.Username,
			DisplayName: impersonator.DisplayName,
		}
	}
	return resp
}

// --- Auth requests / responses ------------------------------------------

// RegisterRequest is the body for POST /v1/auth/register.
type RegisterRequest struct {
	Username    string `json:"username"     validate:"required,min=3,max=32,alphanum" example:"caden"`
	Email       string `json:"email"        validate:"required,email,max=254"         example:"caden@example.com"`
	DisplayName string `json:"display_name" validate:"required,min=1,max=64"          example:"Caden Lund"`
	Password    string `json:"password"     validate:"required,min=8,max=128"         example:"correct-horse-battery-staple"`
}

// RegisterResponse is returned on successful registration. The session
// cookie is also set in the response headers — the Token field is kept
// for future Bearer-token clients (currently unused; cookie is the only
// auth path per §8.2). For now it's empty.
type RegisterResponse struct {
	User MeResponse `json:"user"`
}

// LoginRequest is the body for POST /v1/auth/login.
type LoginRequest struct {
	Identifier string `json:"identifier" validate:"required,min=3,max=254" example:"caden"`
	Password   string `json:"password"   validate:"required,min=1,max=128" example:"correct-horse-battery-staple"`
}

// LoginResponse is the body returned on successful login. Like RegisterResponse,
// the session is bound via cookie — the body is just the authed-self view.
type LoginResponse struct {
	User MeResponse `json:"user"`
}

// PasswordResetRequestRequest is the body for
// POST /v1/auth/password-reset/request. Always-204 contract (§6.2).
type PasswordResetRequestRequest struct {
	Email string `json:"email" validate:"required,email,max=254" example:"caden@example.com"`
}

// PasswordResetConfirmRequest is the body for
// POST /v1/auth/password-reset/confirm.
type PasswordResetConfirmRequest struct {
	Token       string `json:"token"        validate:"required,min=1,max=512" example:"3f8a1c2d9e6b4f2a8d1c7b3e9f5a2c4d3f8a1c2d9e6b4f2a8d1c7b3e9f5a2c4d"`
	NewPassword string `json:"new_password" validate:"required,min=8,max=128" example:"new-correct-horse-battery"`
}
