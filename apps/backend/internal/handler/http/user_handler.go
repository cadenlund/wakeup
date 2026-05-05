package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	notifprefsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	usersvc "github.com/cadenlund/wakeup/apps/backend/internal/service/user"
)

// maxAvatarMultipartBytes is the body cap for /v1/users/me/avatar. We
// add a small slack over the 5 MiB content cap to fit multipart framing
// (boundary + headers). The service still enforces the §4.6 5 MiB cap
// on the file content itself.
const maxAvatarMultipartBytes = usersvc.MaxAvatarBytes + (256 << 10) // 5 MiB + 256 KiB

// UserHandler hosts every /v1/users/* endpoint. Composes the auth, user,
// and notificationpref services. Routes that need a session resolve the
// caller via auth.CurrentUser — the handler-side cookie middleware
// (LoadAndSave) supplies the context.
type UserHandler struct {
	users *usersvc.Service
	auth  *auth.Service
	prefs *notifprefsvc.Service
	v     *validator.Validate
}

// NewUserHandler wires up the handler. Returns an error when any
// dependency is nil.
func NewUserHandler(
	users *usersvc.Service,
	a *auth.Service,
	prefs *notifprefsvc.Service,
	v *validator.Validate,
) (*UserHandler, error) {
	if users == nil {
		return nil, errors.New("httpapi: UserHandler requires non-nil user service")
	}
	if a == nil {
		return nil, errors.New("httpapi: UserHandler requires non-nil auth service")
	}
	if prefs == nil {
		return nil, errors.New("httpapi: UserHandler requires non-nil notificationpref service")
	}
	if v == nil {
		return nil, errors.New("httpapi: UserHandler requires non-nil validator")
	}
	return &UserHandler{users: users, auth: a, prefs: prefs, v: v}, nil
}

// Mount attaches every /v1/users/* route onto r.
func (h *UserHandler) Mount(r chi.Router) {
	r.Route("/v1/users", func(r chi.Router) {
		r.Get("/", h.Search)
		// /me routes come BEFORE /{id} so chi doesn't capture "me" as a UUID.
		r.Patch("/me", h.UpdateMe)
		r.Delete("/me", h.DeleteMe)
		r.Post("/me/avatar", h.UploadAvatar)
		r.Get("/me/notifications", h.GetNotifications)
		r.Patch("/me/notifications", h.UpdateNotifications)
		r.Get("/{id}", h.GetByID)
	})
}

// Search returns up to limit users matching the q query param.
//
// @Summary      Search users
// @Description  Returns up to `limit` users whose username or display_name matches `q` (case-insensitive trigram). Empty `q` returns the catalog ordered by `created_at DESC, id DESC`. Pagination is keyset (§6.4) — never offset.
// @Tags         users
// @Produce      json
// @Security     CookieAuth
// @Param        q       query    string  false  "Search prefix"   example("caden")
// @Param        limit   query    integer false  "Page size (default 20, max 100)"  example(20)
// @Param        cursor  query    string  false  "Opaque pagination cursor from a previous response"  example("eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0=")
// @Success      200     {object} UserListResponse  "Page of users"
// @Header       200     {string} X-Request-ID      "Echoed request id"
// @Failure      400     {object} ErrorResponse     "Invalid limit or cursor"
// @Failure      401     {object} ErrorResponse     "Not authenticated"
// @Failure      429     {object} ErrorResponse     "Rate limited"
// @Failure      500     {object} ErrorResponse     "Internal error"
// @Router       /v1/users [get]
func (h *UserHandler) Search(w http.ResponseWriter, r *http.Request) {
	if _, err := h.auth.CurrentUser(r.Context()); err != nil {
		WriteError(w, r, err)
		return
	}

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

	res, err := h.users.Search(r.Context(), usersvc.SearchParams{
		Query: q, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toUserListResponse(res.Users, res.NextCursor, res.HasMore))
}

// GetByID returns the public profile for the given user id.
//
// @Summary      Get user by id
// @Description  Returns the public profile (username, display_name, avatar). Soft-deleted users collapse to the placeholder shape (§4.6).
// @Tags         users
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string         true  "User id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      200  {object} UserResponse   "Public profile"
// @Header       200  {string} X-Request-ID   "Echoed request id"
// @Failure      400  {object} ErrorResponse  "Malformed id"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      404  {object} ErrorResponse  "User not found"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/users/{id} [get]
func (h *UserHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	if _, err := h.auth.CurrentUser(r.Context()); err != nil {
		WriteError(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	u, err := h.users.GetByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toUserResponse(u))
}

// UpdateMe patches writable fields on the authenticated user.
//
// @Summary      Update current user's profile
// @Description  Patches `display_name`, `avatar_url`, `color_scheme`, `bio`, or `status_emoji`. Each field is optional; omitted (or null) values are left unchanged. Send `""` for `bio`/`status_emoji` to clear them.
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        request  body     UpdateMeRequest  true  "Profile patch"
// @Success      200      {object} MeResponse       "Updated profile"
// @Header       200      {string} X-Request-ID     "Echoed request id"
// @Failure      400      {object} ErrorResponse    "Malformed JSON / empty body"
// @Failure      401      {object} ErrorResponse    "Not authenticated"
// @Failure      413      {object} ErrorResponse    "Request body too large"
// @Failure      422      {object} ErrorResponse    "Validation failed"
// @Failure      429      {object} ErrorResponse    "Rate limited"
// @Failure      500      {object} ErrorResponse    "Internal error"
// @Router       /v1/users/me [patch]
func (h *UserHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var req UpdateMeRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	updated, err := h.users.UpdateProfile(r.Context(), usersvc.UpdateProfileParams{
		UserID:      uid,
		DisplayName: req.DisplayName,
		AvatarURL:   req.AvatarURL,
		ColorScheme: req.ColorScheme,
		Bio:         req.Bio,
		StatusEmoji: req.StatusEmoji,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toMeResponse(updated, nil))
}

// DeleteMe soft-deletes the authenticated user. Per §4.6 their content
// stays; the row's deleted_at gets set and they become invisible.
//
// @Summary      Soft-delete current user
// @Description  Soft-deletes the authenticated user. Their content (messages, etc.) is preserved per §4.6 but they become invisible to lists/search and cannot log in.
// @Tags         users
// @Produce      json
// @Security     CookieAuth
// @Success      204  "No Content"
// @Header       204  {string} X-Request-ID  "Echoed request id"
// @Failure      401  {object} ErrorResponse "Not authenticated"
// @Failure      403  {object} ErrorResponse "Blocked during admin impersonation"
// @Failure      429  {object} ErrorResponse "Rate limited"
// @Failure      500  {object} ErrorResponse "Internal error"
// @Router       /v1/users/me [delete]
func (h *UserHandler) DeleteMe(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if err := h.users.SoftDeleteAccount(r.Context(), uid); err != nil {
		WriteError(w, r, err)
		return
	}
	// Best-effort kill of every session for this user — a stolen cookie
	// post-deletion shouldn't keep working. Errors here aren't fatal: the
	// soft-delete succeeded, which is the security-critical part.
	_ = h.auth.LogoutAll(r.Context(), uid)
	_ = h.auth.Logout(r.Context())
	WriteNoContent(w)
}

// UploadAvatar accepts a multipart `file` field and uploads it to S3.
//
// @Summary      Upload current user's avatar
// @Description  Accepts a multipart form with a single `file` field. Max 5 MiB; server-side MIME detection (§9.2) accepts image/png, image/jpeg, image/gif, image/webp.
// @Tags         users
// @Accept       multipart/form-data
// @Produce      json
// @Security     CookieAuth
// @Param        file     formData file                  true  "Avatar file (max 5 MiB)"
// @Success      200      {object} AvatarUploadResponse  "User with new avatar_url"
// @Header       200      {string} X-Request-ID          "Echoed request id"
// @Failure      400      {object} ErrorResponse         "Missing or empty `file` field"
// @Failure      401      {object} ErrorResponse         "Not authenticated"
// @Failure      413      {object} ErrorResponse         "Avatar exceeds 5 MiB cap"
// @Failure      422      {object} ErrorResponse         "Disallowed content-type"
// @Failure      429      {object} ErrorResponse         "Rate limited"
// @Failure      500      {object} ErrorResponse         "Internal error"
// @Router       /v1/users/me/avatar [post]
func (h *UserHandler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}

	// Defense-in-depth: cap the body before parsing multipart so an
	// attacker can't stream past the 5 MiB limit. The service double-
	// checks via MaxAvatarBytes after reading.
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarMultipartBytes)

	if err := r.ParseMultipartForm(maxAvatarMultipartBytes); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			WriteError(w, r, apierror.PayloadTooLarge("avatar exceeds size cap"))
			return
		}
		WriteError(w, r, apierror.BadRequest("malformed multipart form"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		WriteError(w, r, apierror.BadRequest("missing `file` form field"))
		return
	}
	defer func() { _ = file.Close() }()

	updated, err := h.users.UploadAvatar(r.Context(), uid, file, header.Size)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, AvatarUploadResponse{User: toMeResponse(updated, nil)})
}

// GetNotifications returns the authenticated user's notification toggles.
//
// @Summary      Get current user's notification preferences
// @Description  Returns the per-category push-notification toggles. The row is auto-created with defaults (all true) on first read.
// @Tags         users
// @Produce      json
// @Security     CookieAuth
// @Success      200  {object} NotificationPreferencesResponse  "Notification toggles"
// @Header       200  {string} X-Request-ID                     "Echoed request id"
// @Failure      401  {object} ErrorResponse                    "Not authenticated"
// @Failure      429  {object} ErrorResponse                    "Rate limited"
// @Failure      500  {object} ErrorResponse                    "Internal error"
// @Router       /v1/users/me/notifications [get]
func (h *UserHandler) GetNotifications(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	pref, err := h.prefs.GetForUser(r.Context(), uid)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toNotificationPreferencesResponse(pref))
}

// UpdateNotifications patches whichever category booleans + theme fields are set in the body.
//
// @Summary      Update current user's notification + theme preferences
// @Description  Patches any subset of `direct_messages`, `group_messages`, `friend_requests`, `calls`, `theme_scheme`, `theme_mode_preference`. Omitted fields are unchanged. Theme enum values are validated; invalid values return 400 with a `theme_scheme` / `theme_mode_preference` field error.
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        request  body     UpdateNotificationPreferencesRequest  true  "Preferences patch"
// @Success      200      {object} NotificationPreferencesResponse       "Updated preferences"
// @Header       200      {string} X-Request-ID                          "Echoed request id"
// @Failure      400      {object} ErrorResponse                         "Malformed JSON / empty body / invalid theme enum"
// @Failure      401      {object} ErrorResponse                         "Not authenticated"
// @Failure      413      {object} ErrorResponse                         "Request body too large"
// @Failure      422      {object} ErrorResponse                         "Validation failed"
// @Failure      429      {object} ErrorResponse                         "Rate limited"
// @Failure      500      {object} ErrorResponse                         "Internal error"
// @Router       /v1/users/me/notifications [patch]
func (h *UserHandler) UpdateNotifications(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var req UpdateNotificationPreferencesRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	pref, err := h.prefs.UpdateForUser(r.Context(), notifprefsvc.UpdateParams{
		UserID:              uid,
		DirectMessages:      req.DirectMessages,
		GroupMessages:       req.GroupMessages,
		FriendRequests:      req.FriendRequests,
		Calls:               req.Calls,
		ThemeScheme:         req.ThemeScheme,
		ThemeModePreference: req.ThemeModePreference,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toNotificationPreferencesResponse(pref))
}
