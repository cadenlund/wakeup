package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	adminsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/admin"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
)

// AdminHandler hosts every /v1/admin/* endpoint plus the impersonate-end
// route at /v1/admin/impersonate/end. Wraps the §12.2 admin service plus
// the scs session manager for the §8.7 session-write paths.
type AdminHandler struct {
	admin    *adminsvc.Service
	auth     *auth.Service
	sessions *scs.SessionManager
	v        *validator.Validate
}

// NewAdminHandler wires the handler.
func NewAdminHandler(
	a *adminsvc.Service,
	authSvc *auth.Service,
	sessions *scs.SessionManager,
	v *validator.Validate,
) (*AdminHandler, error) {
	if a == nil {
		return nil, errors.New("httpapi: AdminHandler requires non-nil admin service")
	}
	if authSvc == nil {
		return nil, errors.New("httpapi: AdminHandler requires non-nil auth service")
	}
	if sessions == nil {
		return nil, errors.New("httpapi: AdminHandler requires non-nil session manager")
	}
	if v == nil {
		return nil, errors.New("httpapi: AdminHandler requires non-nil validator")
	}
	return &AdminHandler{admin: a, auth: authSvc, sessions: sessions, v: v}, nil
}

// Mount attaches admin routes onto r. The router is responsible for
// stacking RequireAdmin upstream — this handler trusts that gate.
func (h *AdminHandler) Mount(r chi.Router) {
	r.Get("/v1/admin/users", h.ListUsers)
	r.Get("/v1/admin/users/{id}", h.GetUser)
	r.Patch("/v1/admin/users/{id}", h.UpdateUser)
	r.Post("/v1/admin/users/{id}/impersonate", h.StartImpersonation)
	r.Post("/v1/admin/impersonate/end", h.EndImpersonation)
	r.Get("/v1/admin/audit", h.ListAudit)
}

// ListUsers returns paginated active users.
//
// @Summary      List users (admin)
// @Description  Paginated trigram-prefix search across active (non soft-deleted) users. Soft-deleted users can be retrieved individually via GET /v1/admin/users/{id}. Same cursor envelope as the public /v1/users endpoint.
// @Tags         admin
// @Produce      json
// @Security     CookieAuth
// @Param        q       query    string  false  "Search prefix"   example("caden")
// @Param        limit   query    integer false  "Page size (default 20, max 100)"  example(20)
// @Param        cursor  query    string  false  "Opaque pagination cursor"  example("eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0=")
// @Success      200  {object} AdminUserListResponse
// @Failure      400  {object} ErrorResponse  "Invalid limit or cursor"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      403  {object} ErrorResponse  "Admin role required"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/admin/users [get]
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 200 {
		WriteError(w, r, apierror.BadRequest("q exceeds 200 chars"))
		return
	}
	limit, err := pagination.ParseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	res, err := h.admin.ListUsers(r.Context(), adminsvc.ListUsersParams{
		Query: q, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, AdminUserListResponse{
		Data: toAdminUserList(res.Users), NextCursor: res.NextCursor, HasMore: res.HasMore,
	})
}

// GetUser returns the admin view of one user, including soft-deleted rows.
//
// @Summary      Get user by id (admin)
// @Description  Returns the full row for the given user, including soft-deleted users (their `deleted_at` will be non-null). Use this when an admin needs to inspect a deleted account.
// @Tags         admin
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "User id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      200  {object} AdminUserResponse
// @Failure      400  {object} ErrorResponse  "Malformed id"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      403  {object} ErrorResponse  "Admin role required"
// @Failure      404  {object} ErrorResponse  "User not found"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/admin/users/{id} [get]
func (h *AdminHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	u, err := h.admin.GetUser(r.Context(), id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toAdminUserResponse(u))
}

// UpdateUser patches role and/or soft-delete state.
//
// @Summary      Update user (admin)
// @Description  Patches `role` (promote/demote) and/or `deleted_at` (set to now() to soft-delete). Both fields optional and may appear in the same call. Restoring a soft-deleted user (sending `deleted_at: null`) is intentionally NOT supported here yet — it returns 422.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string                  true  "User id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     UpdateAdminUserRequest  true  "Patch fields"
// @Success      200      {object} AdminUserResponse
// @Failure      400      {object} ErrorResponse  "Malformed JSON or id"
// @Failure      401      {object} ErrorResponse  "Not authenticated"
// @Failure      403      {object} ErrorResponse  "Admin role required"
// @Failure      404      {object} ErrorResponse  "User not found"
// @Failure      413      {object} ErrorResponse  "Request body too large"
// @Failure      422      {object} ErrorResponse  "Validation failed"
// @Failure      429      {object} ErrorResponse  "Rate limited"
// @Failure      500      {object} ErrorResponse  "Internal error"
// @Router       /v1/admin/users/{id} [patch]
func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	actor := mw.RealUserFromContext(r.Context())
	if actor == nil {
		WriteError(w, r, apierror.Unauthorized("not authenticated"))
		return
	}
	var req UpdateAdminUserRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	// Restoring a soft-deleted user is not supported in 12.5; reject
	// `"deleted_at": null` explicitly so callers see a clear 422 instead
	// of silently no-oping the field.
	if req.IsRestoreAttempt() {
		WriteError(w, r, apierror.Validation([]apierror.FieldError{{
			Field: "deleted_at", Code: "INVALID_VALUE",
			Message: "restoring soft-deleted users is not supported",
		}}))
		return
	}

	// Role + soft-delete commit in one transaction inside the service so
	// a partial failure can't leave the row half-updated. The service
	// also enforces "at least one field must be supplied" → 422.
	updated, err := h.admin.UpdateUser(r.Context(), adminsvc.UpdateUserParams{
		ActorID:    actor.ID,
		UserID:     id,
		Role:       req.Role,
		SoftDelete: req.WantsSoftDelete(),
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toAdminUserResponse(updated))
}

// StartImpersonation puts impersonating_user_id into the admin's session
// and returns the target's MeResponse with impersonated_by populated.
//
// @Summary      Start impersonating a user (admin)
// @Description  Per §8.7: validates the restriction matrix, writes the audit `impersonate.started` bookend, and stores `impersonating_user_id` on the admin's existing session via scs.Manager.Put. Subsequent requests resolve `ctx.User` to the target via the auth middleware. Returns the target's MeResponse with `impersonated_by` set.
// @Tags         admin
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Target user id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      200  {object} MeResponse  "Target's MeResponse with impersonated_by set"
// @Failure      400  {object} ErrorResponse  "Malformed id"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      403  {object} ErrorResponse  "Admin role required, or target is admin"
// @Failure      404  {object} ErrorResponse  "Target not found or soft-deleted"
// @Failure      422  {object} ErrorResponse  "Cannot impersonate yourself"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/admin/users/{id}/impersonate [post]
func (h *AdminHandler) StartImpersonation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	actor := mw.RealUserFromContext(r.Context())
	if actor == nil {
		WriteError(w, r, apierror.Unauthorized("not authenticated"))
		return
	}
	target, err := h.admin.StartImpersonation(r.Context(), adminsvc.StartImpersonationParams{
		ActorID: actor.ID, TargetID: id,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	// Persist the target id on the admin's session. scs.LoadAndSave will
	// flush the change in the response Set-Cookie before write.
	h.sessions.Put(r.Context(), mw.SessionImpersonatingKey, id.String())
	WriteJSON(w, http.StatusOK, toMeResponse(target, actor))
}

// EndImpersonation clears the impersonation field and returns the
// admin's own MeResponse. Idempotent — calling without an active
// impersonation returns the admin's MeResponse without writing an
// audit row (no bookend pair to close).
//
// @Summary      End impersonation (admin)
// @Description  Clears `impersonating_user_id` from the admin's session via scs.Manager.Remove and writes the audit `impersonate.ended` bookend. Idempotent: with no active impersonation, returns 200 with the admin's MeResponse and writes no audit row.
// @Tags         admin
// @Produce      json
// @Security     CookieAuth
// @Success      200  {object} MeResponse  "Admin's own MeResponse"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      403  {object} ErrorResponse  "Admin role required"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/admin/impersonate/end [post]
func (h *AdminHandler) EndImpersonation(w http.ResponseWriter, r *http.Request) {
	actor := mw.RealUserFromContext(r.Context())
	if actor == nil {
		WriteError(w, r, apierror.Unauthorized("not authenticated"))
		return
	}
	rawTarget := h.sessions.GetString(r.Context(), mw.SessionImpersonatingKey)
	if rawTarget != "" {
		if targetID, perr := uuid.Parse(rawTarget); perr == nil {
			if err := h.admin.EndImpersonation(r.Context(), adminsvc.EndImpersonationParams{
				ActorID: actor.ID, TargetID: targetID,
			}); err != nil {
				WriteError(w, r, err)
				return
			}
		}
		h.sessions.Remove(r.Context(), mw.SessionImpersonatingKey)
	}
	WriteJSON(w, http.StatusOK, toMeResponse(*actor, nil))
}

// ListAudit returns paginated audit_log rows.
//
// @Summary      List audit log (admin)
// @Description  Returns audit_log rows newest-first, keyset-paginated. Same envelope as /v1/users.
// @Tags         admin
// @Produce      json
// @Security     CookieAuth
// @Param        limit   query    integer false  "Page size (default 20, max 100)"  example(20)
// @Param        cursor  query    string  false  "Opaque pagination cursor"  example("eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0=")
// @Success      200  {object} AuditLogListResponse
// @Failure      400  {object} ErrorResponse  "Invalid limit or cursor"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      403  {object} ErrorResponse  "Admin role required"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/admin/audit [get]
func (h *AdminHandler) ListAudit(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.ParseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	res, err := h.admin.ListAudit(r.Context(), adminsvc.ListAuditParams{
		Cursor: cursor, Limit: limit,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, AuditLogListResponse{
		Data: toAuditLogList(res.Entries), NextCursor: res.NextCursor, HasMore: res.HasMore,
	})
}
