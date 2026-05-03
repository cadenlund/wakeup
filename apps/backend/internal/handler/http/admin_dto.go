package httpapi

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// --- /v1/admin/users -----------------------------------------------------

// AdminUserResponse renders the admin's view of one user. Includes
// `email`, `role`, and `deleted_at` (none of which surface on
// UserResponse) so the admin tooling can act on the full row.
type AdminUserResponse struct {
	ID          uuid.UUID  `json:"id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Username    string     `json:"username"     example:"caden"`
	DisplayName string     `json:"display_name" example:"Caden Lund"`
	Email       string     `json:"email"        example:"caden@example.com"`
	AvatarURL   *string    `json:"avatar_url"   example:"https://wakeup.app/avatars/caden.png"`
	Role        string     `json:"role"         example:"user"`
	CreatedAt   time.Time  `json:"created_at"   example:"2026-05-02T09:31:21.810Z"`
	DeletedAt   *time.Time `json:"deleted_at"   example:"2026-05-02T09:31:21.810Z"`
}

// AdminUserListResponse is the body of GET /v1/admin/users.
type AdminUserListResponse struct {
	Data       []AdminUserResponse `json:"data"`
	NextCursor *string             `json:"next_cursor"  example:"eyJ0cyI6Ii4uLiJ9"`
	HasMore    bool                `json:"has_more"     example:"false"`
}

// UpdateAdminUserRequest is the body of PATCH /v1/admin/users/{id}.
//
// Both fields are optional and may appear in the same request. The
// `deleted_at` field uses *json.RawMessage so the handler can tell
// three states apart that `*time.Time` collapses into nil:
//
//	field omitted        → DeletedAt == nil               → no change
//	"deleted_at": null   → DeletedAt points at "null"     → 422 (restore not supported in this milestone)
//	"deleted_at": "<ts>" → DeletedAt points at quoted ts  → soft-delete (server stamps now(); the value the client sends is ignored)
//
// Type-system note: a sibling `*time.Time` field cannot distinguish the
// first two cases, which is why we drop down to RawMessage here.
type UpdateAdminUserRequest struct {
	Role      *string          `json:"role,omitempty"       validate:"omitempty,oneof=user admin" example:"admin"`
	DeletedAt *json.RawMessage `json:"deleted_at,omitempty" swaggertype:"string"                  example:"2026-05-02T09:31:21.810Z"`
}

// IsRestoreAttempt reports whether the request asked for `"deleted_at": null`.
// Restore isn't supported in milestone 12.5; the handler maps this to 422.
func (r UpdateAdminUserRequest) IsRestoreAttempt() bool {
	if r.DeletedAt == nil {
		return false
	}
	// json.RawMessage of `null` is the literal four bytes "null".
	return string(*r.DeletedAt) == "null"
}

// WantsSoftDelete reports whether the caller asked to set deleted_at
// to a concrete (non-null) value. The actual timestamp the caller sends
// is ignored — the repo stamps now() server-side.
func (r UpdateAdminUserRequest) WantsSoftDelete() bool {
	if r.DeletedAt == nil {
		return false
	}
	return !r.IsRestoreAttempt()
}

// toAdminUserResponse converts a domain.User into the admin-view shape.
// Soft-deleted rows pass through verbatim so admins can see their state.
func toAdminUserResponse(u domain.User) AdminUserResponse {
	return AdminUserResponse{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
		Email: u.Email, AvatarURL: u.AvatarURL, Role: u.Role,
		CreatedAt: u.CreatedAt, DeletedAt: u.DeletedAt,
	}
}

func toAdminUserList(users []domain.User) []AdminUserResponse {
	out := make([]AdminUserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, toAdminUserResponse(u))
	}
	return out
}

// --- /v1/admin/audit -----------------------------------------------------

// AuditLogResponse renders one audit_log row.
type AuditLogResponse struct {
	ID         uuid.UUID      `json:"id"          example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	ActorID    *uuid.UUID     `json:"actor_id"    example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Action     string         `json:"action"      example:"user.update_role"`
	TargetType *string        `json:"target_type" example:"user"`
	TargetID   *uuid.UUID     `json:"target_id"   example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	Metadata   map[string]any `json:"metadata"    swaggertype:"object"`
	CreatedAt  time.Time      `json:"created_at"  example:"2026-05-02T09:31:21.810Z"`
}

// AuditLogListResponse is the body of GET /v1/admin/audit.
type AuditLogListResponse struct {
	Data       []AuditLogResponse `json:"data"`
	NextCursor *string            `json:"next_cursor" example:"eyJ0cyI6Ii4uLiJ9"`
	HasMore    bool               `json:"has_more"    example:"false"`
}

func toAuditLogResponse(a domain.AuditLog) AuditLogResponse {
	return AuditLogResponse{
		ID: a.ID, ActorID: a.ActorID, Action: a.Action,
		TargetType: a.TargetType, TargetID: a.TargetID,
		Metadata: a.Metadata, CreatedAt: a.CreatedAt,
	}
}

func toAuditLogList(rows []domain.AuditLog) []AuditLogResponse {
	out := make([]AuditLogResponse, 0, len(rows))
	for _, a := range rows {
		out = append(out, toAuditLogResponse(a))
	}
	return out
}
