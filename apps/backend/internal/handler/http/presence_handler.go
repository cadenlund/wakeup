package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	presencesvc "github.com/cadenlund/wakeup/apps/backend/internal/service/presence"
	usersvc "github.com/cadenlund/wakeup/apps/backend/internal/service/user"
)

// PresenceHandler hosts /v1/presence/* and /v1/widget/friends
// endpoints. Composes the presence service + user service so the
// widget endpoint can embed user profiles without a follow-up call.
type PresenceHandler struct {
	presence *presencesvc.Service
	users    *usersvc.Service
	auth     *auth.Service
	v        *validator.Validate
}

// NewPresenceHandler wires the handler.
func NewPresenceHandler(
	presence *presencesvc.Service,
	users *usersvc.Service,
	a *auth.Service,
	v *validator.Validate,
) (*PresenceHandler, error) {
	if presence == nil {
		return nil, errors.New("httpapi: PresenceHandler requires non-nil presence service")
	}
	if users == nil {
		return nil, errors.New("httpapi: PresenceHandler requires non-nil user service")
	}
	if a == nil {
		return nil, errors.New("httpapi: PresenceHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: PresenceHandler requires non-nil validator")
	}
	return &PresenceHandler{presence: presence, users: users, auth: a, v: v}, nil
}

// Mount attaches presence + widget routes onto r.
func (h *PresenceHandler) Mount(r chi.Router) {
	r.Get("/v1/presence/friends", h.ListFriendsPresence)
	r.Post("/v1/presence/status", h.SetStatus)
	r.Get("/v1/widget/friends", h.WidgetFriends)
}

// ListFriendsPresence returns the presence state of every friend.
//
// @Summary      List friends' presence
// @Description  Returns one row per accepted friend with their current presence status (`online` / `away` / `offline` / `sleeping`) and `last_active_at`. Friends with no row yet surface as `offline`. Used on app open to populate the friend list immediately; the realtime stream then updates incrementally via §7.2 `presence.update`.
// @Tags         presence
// @Produce      json
// @Security     CookieAuth
// @Success      200  {object} PresenceListResponse  "Friends' presence"
// @Header       200  {string} X-Request-ID          "Echoed request id"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/presence/friends [get]
func (h *PresenceHandler) ListFriendsPresence(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rows, err := h.presence.ListFriends(r.Context(), uid)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, PresenceListResponse{Data: toPresenceList(rows)})
}

// SetStatus is the manual override (§7.3 presence.set's REST sibling).
//
// @Summary      Set my presence status
// @Description  Manual override for the user's status. Only `online` and `sleeping` are user-settable — the decay sweeper handles `away` / `offline` automatically (§9.2). On a real change, the server publishes `presence.update` to every accepted friend (§7.2).
// @Tags         presence
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        request  body     SetPresenceStatusRequest  true  "Status to set"
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed JSON"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      413  {object}  ErrorResponse "Request body too large"
// @Failure      422  {object}  ErrorResponse "Validation failed"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/presence/status [post]
func (h *PresenceHandler) SetStatus(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var req SetPresenceStatusRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	if err := h.presence.SetStatus(r.Context(), uid, domain.PresenceStatus(req.Status)); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// WidgetFriends returns a compact friend-list view enriched with presence.
//
// @Summary      Widget feed (friends + presence)
// @Description  Mobile widget endpoint per §6.1. Returns one row per accepted friend, embedding the public user profile alongside their presence state — designed for a low-frequency poll (~15min) that wants everything for the widget UI in one shape.
// @Tags         presence
// @Produce      json
// @Security     CookieAuth
// @Success      200  {object} WidgetFriendsResponse  "Friends with embedded presence"
// @Header       200  {string} X-Request-ID           "Echoed request id"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/widget/friends [get]
func (h *PresenceHandler) WidgetFriends(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	presence, err := h.presence.ListFriends(r.Context(), uid)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if len(presence) == 0 {
		WriteJSON(w, http.StatusOK, WidgetFriendsResponse{Data: []WidgetFriendRow{}})
		return
	}
	rendered, err := h.renderWidget(r.Context(), presence)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, WidgetFriendsResponse{Data: rendered})
}

// renderWidget batch-loads user profiles for the friends in the
// presence list and zips them together. One round-trip per page —
// the same N+1-avoidance pattern PR #36 settled for conversation
// member rendering.
func (h *PresenceHandler) renderWidget(ctx context.Context, presence []domain.PresenceState) ([]WidgetFriendRow, error) {
	ids := make([]uuid.UUID, 0, len(presence))
	for _, p := range presence {
		ids = append(ids, p.UserID)
	}
	users, err := h.users.ListByIDs(ctx, ids)
	if err != nil {
		return nil, apierror.Internal("widget: list users").WithCause(err)
	}
	usersByID := make(map[uuid.UUID]domain.User, len(users))
	for _, u := range users {
		usersByID[u.ID] = u
	}
	out := make([]WidgetFriendRow, 0, len(presence))
	for _, p := range presence {
		u, ok := usersByID[p.UserID]
		if !ok {
			// FK cascade should make this impossible, but render the
			// §4.6 placeholder rather than a half-empty row.
			u = domain.User{ID: p.UserID}
		}
		out = append(out, WidgetFriendRow{
			User:     toUserResponse(u),
			Presence: toPresenceResponse(p),
		})
	}
	return out, nil
}
