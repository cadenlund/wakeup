package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	roomsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/room"
)

// RoomHandler hosts /v1/conversations/{id}/room/{join|leave} +
// GET /v1/conversations/{id}/room. Wraps the §10.2 room service.
type RoomHandler struct {
	rooms *roomsvc.Service
	auth  *auth.Service
	v     *validator.Validate
}

// NewRoomHandler wires the handler.
func NewRoomHandler(rooms *roomsvc.Service, a *auth.Service, v *validator.Validate) (*RoomHandler, error) {
	if rooms == nil {
		return nil, errors.New("httpapi: RoomHandler requires non-nil room service")
	}
	if a == nil {
		return nil, errors.New("httpapi: RoomHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: RoomHandler requires non-nil validator")
	}
	return &RoomHandler{rooms: rooms, auth: a, v: v}, nil
}

// Mount attaches the room routes onto r.
func (h *RoomHandler) Mount(r chi.Router) {
	r.Post("/v1/conversations/{id}/room/join", h.Join)
	r.Post("/v1/conversations/{id}/room/leave", h.Leave)
	r.Get("/v1/conversations/{id}/room", h.Get)
}

// Join issues a LiveKit JWT for the conversation room.
//
// @Summary      Join a conversation's room
// @Description  Issues a LiveKit JWT scoped to this conversation's persistent room (§10.3 — the room name is `conv:<conversation_id>`, returned verbatim in the response's `room_id` field; the prefix lets ops tell call rooms apart from any other LiveKit room name we may add later). The caller must be a member; non-members get 404 (no enumeration leak). Token TTL is 10 minutes; LiveKit auto-refreshes during the connection. The `video` flag is a UI hint baked into the JWT metadata so other participants can render the camera-on indicator — token publish permissions are identical regardless.
// @Tags         rooms
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string            true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     JoinRoomRequest   false  "Join hints"
// @Success      200      {object} JoinRoomResponse  "LiveKit connection details"
// @Header       200      {string} X-Request-ID      "Echoed request id"
// @Failure      400      {object} ErrorResponse  "Malformed JSON or id"
// @Failure      401      {object} ErrorResponse  "Not authenticated"
// @Failure      404      {object} ErrorResponse  "Conversation not found or caller not a member"
// @Failure      413      {object} ErrorResponse  "Request body too large"
// @Failure      422      {object} ErrorResponse  "Validation failed"
// @Failure      429      {object} ErrorResponse  "Rate limited"
// @Failure      500      {object} ErrorResponse  "Internal error"
// @Router       /v1/conversations/{id}/room/join [post]
func (h *RoomHandler) Join(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	convID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	// Empty body is valid (video defaults to false). DecodeJSON
	// rejects an empty body; gate on Content-Length so a no-body
	// POST goes through with the zero-value request.
	var req JoinRoomRequest
	if r.ContentLength > 0 {
		if e := DecodeJSON(r, h.v, &req); e != nil {
			WriteError(w, r, e)
			return
		}
	}
	res, err := h.rooms.Join(r.Context(), uid, convID, req.Video)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toJoinRoomResponse(res))
}

// Leave is the best-effort cleanup endpoint.
//
// @Summary      Leave a conversation's room
// @Description  Best-effort cleanup — the LiveKit `participant_left` webhook is the source of truth for the participant set, so this endpoint exists primarily to give the client a stable place to record an explicit leave intent. Membership is still checked so non-members can't poke at room state.
// @Tags         rooms
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed id"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      404  {object}  ErrorResponse "Conversation not found or caller not a member"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/conversations/{id}/room/leave [post]
func (h *RoomHandler) Leave(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	convID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	if err := h.rooms.Leave(r.Context(), uid, convID); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// Get returns the current participants + started_at for the room.
//
// @Summary      Get a conversation's room state
// @Description  Returns the current participant list (with `joined_at` and `video` flags) plus `started_at` for the conversation's persistent room (§10.3). Membership-gated; non-members get 404.
// @Tags         rooms
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string             true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      200  {object} RoomStateResponse  "Current participants + started_at"
// @Header       200  {string} X-Request-ID       "Echoed request id"
// @Failure      400  {object} ErrorResponse  "Malformed id"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      404  {object} ErrorResponse  "Conversation not found or caller not a member"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/conversations/{id}/room [get]
func (h *RoomHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	convID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	state, err := h.rooms.GetParticipants(r.Context(), uid, convID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toRoomStateResponse(state))
}
