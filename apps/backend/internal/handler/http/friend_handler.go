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
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	friendsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/friend"
	usersvc "github.com/cadenlund/wakeup/apps/backend/internal/service/user"
)

// FriendHandler hosts every /v1/friends/* endpoint. Composes the friend
// service (§4.2) with the auth + user services so handler responses can
// render the counterparty's public profile inline.
type FriendHandler struct {
	friends *friendsvc.Service
	users   *usersvc.Service
	auth    *auth.Service
	v       *validator.Validate
}

// NewFriendHandler wires up the handler. Returns an error when any
// dependency is nil.
func NewFriendHandler(friends *friendsvc.Service, users *usersvc.Service, a *auth.Service, v *validator.Validate) (*FriendHandler, error) {
	if friends == nil {
		return nil, errors.New("httpapi: FriendHandler requires non-nil friend service")
	}
	if users == nil {
		return nil, errors.New("httpapi: FriendHandler requires non-nil user service")
	}
	if a == nil {
		return nil, errors.New("httpapi: FriendHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: FriendHandler requires non-nil validator")
	}
	return &FriendHandler{friends: friends, users: users, auth: a, v: v}, nil
}

// Mount attaches every /v1/friends/* route onto r.
func (h *FriendHandler) Mount(r chi.Router) {
	r.Route("/v1/friends", func(r chi.Router) {
		r.Get("/", h.List)
		// /requests routes come BEFORE /{user_id} so chi doesn't
		// capture "requests" as a UUID.
		r.Get("/requests", h.ListRequests)
		r.Post("/requests", h.SendRequest)
		r.Post("/requests/{id}/accept", h.AcceptRequest)
		r.Post("/requests/{id}/decline", h.DeclineRequest)
		r.Delete("/{user_id}", h.Unfriend)
		r.Post("/{user_id}/block", h.Block)
		r.Delete("/{user_id}/block", h.Unblock)
	})
}

// List returns the authenticated user's accepted friendships.
//
// @Summary      List accepted friends
// @Description  Returns the caller's accepted friendships keyset-paginated by `accepted_at DESC, id DESC` per §6.4. Each row embeds the counterparty's public profile.
// @Tags         friends
// @Produce      json
// @Security     CookieAuth
// @Param        limit   query    integer  false  "Page size (default 20, max 100)"  example(20)
// @Param        cursor  query    string   false  "Opaque pagination cursor from a previous response"  example("eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0=")
// @Success      200  {object} FriendListResponse  "Page of friendships"
// @Header       200  {string} X-Request-ID        "Echoed request id"
// @Failure      400  {object} ErrorResponse       "Invalid limit or cursor"
// @Failure      401  {object} ErrorResponse       "Not authenticated"
// @Failure      429  {object} ErrorResponse       "Rate limited"
// @Failure      500  {object} ErrorResponse       "Internal error"
// @Router       /v1/friends [get]
func (h *FriendHandler) List(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
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
	res, err := h.friends.ListFriends(r.Context(), friendsvc.ListFriendsParams{
		UserID: uid, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderFriendships(r.Context(), uid, res.Friendships)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, FriendListResponse{
		Data: rendered, NextCursor: res.NextCursor, HasMore: res.HasMore,
	})
}

// ListRequests returns pending requests partitioned by direction.
//
// @Summary      List pending friend requests
// @Description  Returns the caller's pending friend requests partitioned into `incoming` (where the caller is the addressee) and `outgoing` (where the caller is the requester).
// @Tags         friends
// @Produce      json
// @Security     CookieAuth
// @Success      200  {object} FriendRequestsResponse  "Pending requests"
// @Header       200  {string} X-Request-ID            "Echoed request id"
// @Failure      401  {object} ErrorResponse           "Not authenticated"
// @Failure      429  {object} ErrorResponse           "Rate limited"
// @Failure      500  {object} ErrorResponse           "Internal error"
// @Router       /v1/friends/requests [get]
func (h *FriendHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	res, err := h.friends.ListRequests(r.Context(), uid)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	incoming, err := h.renderFriendships(r.Context(), uid, res.Incoming)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	outgoing, err := h.renderFriendships(r.Context(), uid, res.Outgoing)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, FriendRequestsResponse{
		Incoming: incoming, Outgoing: outgoing,
	})
}

// SendRequest creates a pending friend request from the caller to the
// user with the given username.
//
// @Summary      Send a friend request
// @Description  Creates a `pending` friendship from the caller to the user with `username`. Returns 409 if a friendship already exists in either direction (including blocks — the response is generic so block existence isn't leaked).
// @Tags         friends
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        request  body     SendFriendRequestRequest  true  "Target username"
// @Success      201      {object} FriendshipResponse        "Pending friendship"
// @Header       201      {string} X-Request-ID              "Echoed request id"
// @Failure      400      {object} ErrorResponse             "Malformed JSON / empty body"
// @Failure      401      {object} ErrorResponse             "Not authenticated"
// @Failure      404      {object} ErrorResponse             "Target user not found"
// @Failure      409      {object} ErrorResponse             "Friendship already exists or blocked"
// @Failure      413      {object} ErrorResponse             "Request body too large"
// @Failure      422      {object} ErrorResponse             "Validation failed"
// @Failure      429      {object} ErrorResponse             "Rate limited"
// @Failure      500      {object} ErrorResponse             "Internal error"
// @Router       /v1/friends/requests [post]
func (h *FriendHandler) SendRequest(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var req SendFriendRequestRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	created, err := h.friends.SendRequest(r.Context(), uid, req.Username)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderFriendship(r.Context(), uid, created)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, rendered)
}

// AcceptRequest transitions a pending row to accepted. Only the
// addressee may accept.
//
// @Summary      Accept a friend request
// @Tags         friends
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Friendship id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      200  {object} FriendshipResponse  "Accepted friendship"
// @Header       200  {string} X-Request-ID        "Echoed request id"
// @Failure      400  {object} ErrorResponse       "Malformed friendship id"
// @Failure      401  {object} ErrorResponse       "Not authenticated"
// @Failure      403  {object} ErrorResponse       "Caller is not the addressee"
// @Failure      404  {object} ErrorResponse       "Friend request not found"
// @Failure      409  {object} ErrorResponse       "Friend request is not pending"
// @Failure      429  {object} ErrorResponse       "Rate limited"
// @Failure      500  {object} ErrorResponse       "Internal error"
// @Router       /v1/friends/requests/{id}/accept [post]
func (h *FriendHandler) AcceptRequest(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	updated, err := h.friends.AcceptRequest(r.Context(), uid, id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderFriendship(r.Context(), uid, updated)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, rendered)
}

// DeclineRequest deletes a pending row. Only the addressee may decline.
//
// @Summary      Decline a friend request
// @Tags         friends
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Friendship id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed friendship id"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      403  {object}  ErrorResponse "Caller is not the addressee"
// @Failure      404  {object}  ErrorResponse "Friend request not found"
// @Failure      409  {object}  ErrorResponse "Friend request is not pending"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/friends/requests/{id}/decline [post]
func (h *FriendHandler) DeclineRequest(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	if err := h.friends.DeclineRequest(r.Context(), uid, id); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// Unfriend deletes an accepted friendship. Either side can unfriend.
//
// @Summary      Unfriend a user
// @Tags         friends
// @Produce      json
// @Security     CookieAuth
// @Param        user_id  path     string  true  "Counterparty user id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed user id"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      404  {object}  ErrorResponse "No friendship between these users"
// @Failure      409  {object}  ErrorResponse "Friendship is pending or blocked, not accepted"
// @Failure      422  {object}  ErrorResponse "Cannot unfriend yourself"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/friends/{user_id} [delete]
func (h *FriendHandler) Unfriend(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	other, err := uuid.Parse(chi.URLParam(r, "user_id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("user_id must be a valid UUID"))
		return
	}
	if err := h.friends.Unfriend(r.Context(), uid, other); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// Block records the caller as having blocked the target user.
//
// @Summary      Block a user
// @Description  Records the caller as having blocked the target. Replaces any pending/accepted friendship between the two. Refuses (403) if the target has already blocked the caller — the existing block stays in place and the response is intentionally generic.
// @Tags         friends
// @Produce      json
// @Security     CookieAuth
// @Param        user_id  path     string  true  "User id to block (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      201  {object}  FriendshipResponse  "Block row"
// @Header       201  {string}  X-Request-ID        "Echoed request id"
// @Failure      400  {object}  ErrorResponse       "Malformed user id"
// @Failure      401  {object}  ErrorResponse       "Not authenticated"
// @Failure      403  {object}  ErrorResponse       "Target has already blocked the caller"
// @Failure      404  {object}  ErrorResponse       "Target user not found"
// @Failure      409  {object}  ErrorResponse       "Concurrent write conflict; retry"
// @Failure      422  {object}  ErrorResponse       "Cannot block yourself"
// @Failure      429  {object}  ErrorResponse       "Rate limited"
// @Failure      500  {object}  ErrorResponse       "Internal error"
// @Router       /v1/friends/{user_id}/block [post]
func (h *FriendHandler) Block(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	other, err := uuid.Parse(chi.URLParam(r, "user_id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("user_id must be a valid UUID"))
		return
	}
	created, err := h.friends.Block(r.Context(), uid, other)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderFriendship(r.Context(), uid, created)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, rendered)
}

// Unblock removes the caller's block on the target user.
//
// @Summary      Unblock a user
// @Description  Removes the caller's block on the target user. The target party can't call this — only the original blocker can.
// @Tags         friends
// @Produce      json
// @Security     CookieAuth
// @Param        user_id  path     string  true  "User id to unblock (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed user id"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      404  {object}  ErrorResponse "No block by the caller exists for this user"
// @Failure      422  {object}  ErrorResponse "Cannot unblock yourself"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/friends/{user_id}/block [delete]
func (h *FriendHandler) Unblock(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	other, err := uuid.Parse(chi.URLParam(r, "user_id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("user_id must be a valid UUID"))
		return
	}
	if err := h.friends.Unblock(r.Context(), uid, other); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// renderFriendship enriches a single friendship with the counter-party's
// public profile.
func (h *FriendHandler) renderFriendship(ctx context.Context, self uuid.UUID, f domain.Friendship) (FriendshipResponse, error) {
	otherID := f.OtherID(self)
	users, err := h.users.ListByIDs(ctx, []uuid.UUID{otherID})
	if err != nil {
		return FriendshipResponse{}, err
	}
	var other domain.User
	if len(users) > 0 {
		other = users[0]
	} else {
		// Counterparty user is missing — shouldn't happen with FK
		// cascade in place, but render the §4.6 placeholder so the
		// client doesn't see a half-empty payload.
		other = domain.User{ID: otherID}
	}
	return toFriendshipResponse(f, other), nil
}

// renderFriendships batch-loads counterparties for a list of friendships.
func (h *FriendHandler) renderFriendships(ctx context.Context, self uuid.UUID, fs []domain.Friendship) ([]FriendshipResponse, error) {
	if len(fs) == 0 {
		return []FriendshipResponse{}, nil
	}
	ids := make([]uuid.UUID, 0, len(fs))
	for _, f := range fs {
		ids = append(ids, f.OtherID(self))
	}
	users, err := h.users.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]domain.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	out := make([]FriendshipResponse, 0, len(fs))
	for _, f := range fs {
		other, ok := byID[f.OtherID(self)]
		if !ok {
			other = domain.User{ID: f.OtherID(self)}
		}
		out = append(out, toFriendshipResponse(f, other))
	}
	return out, nil
}
