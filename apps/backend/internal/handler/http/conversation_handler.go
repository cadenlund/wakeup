package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	usersvc "github.com/cadenlund/wakeup/apps/backend/internal/service/user"
)

// ConvMessageData is the slice of the message service this handler
// needs to enrich each ConversationResponse with per-row message
// state — the unread count and the last-message preview. Defined
// locally so tests can stub it; the production wiring uses
// *message.Service (handler → service → repository).
type ConvMessageData interface {
	CountUnreadByConversation(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]int64, error)
	LatestMessageByConversation(ctx context.Context, convIDs []uuid.UUID) (map[uuid.UUID]domain.Message, error)
}

// ConversationHandler hosts every /v1/conversations/* endpoint
// (excluding /messages, which lands in milestone 6.x). Composes the
// conversation service + user service so member rows can be rendered
// with their public profiles inline.
type ConversationHandler struct {
	convs   *convsvc.Service
	users   *usersvc.Service
	auth    *auth.Service
	msgs    ConvMessageData // optional; nil → unread_count is 0 and last_message is null
	v       *validator.Validate
	presign Presigner // optional; nil → raw avatar keys
}

// NewConversationHandler wires up the handler.
//
// `msgs` is optional; when nil, every ConversationResponse reports
// unread_count = 0 and last_message = null (graceful degradation — the
// per-row badge / preview just won't show until it's wired in).
func NewConversationHandler(
	convs *convsvc.Service,
	users *usersvc.Service,
	a *auth.Service,
	msgs ConvMessageData,
	v *validator.Validate,
	presign Presigner,
) (*ConversationHandler, error) {
	if convs == nil {
		return nil, errors.New("httpapi: ConversationHandler requires non-nil conversation service")
	}
	if users == nil {
		return nil, errors.New("httpapi: ConversationHandler requires non-nil user service")
	}
	if a == nil {
		return nil, errors.New("httpapi: ConversationHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: ConversationHandler requires non-nil validator")
	}
	return &ConversationHandler{convs: convs, users: users, auth: a, msgs: msgs, v: v, presign: presign}, nil
}

// Mount attaches every /v1/conversations/* route onto r.
func (h *ConversationHandler) Mount(r chi.Router) {
	r.Route("/v1/conversations", func(r chi.Router) {
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Patch("/{id}", h.Update)
		r.Delete("/{id}", h.Leave)
		r.Post("/{id}/members", h.AddMembers)
		r.Delete("/{id}/members/{user_id}", h.RemoveMember)
		r.Post("/{id}/read", h.MarkRead)
		r.Patch("/{id}/mute", h.SetMute)
		r.Patch("/{id}/pin", h.SetPin)
	})
}

// List returns the caller's conversations keyset-paginated by
// last_message_at DESC.
//
// @Summary      List conversations
// @Description  Returns the caller's conversations keyset-paginated by `last_message_at DESC, id DESC` per §6.4. Each row embeds the full member list with public profiles.
// @Tags         conversations
// @Produce      json
// @Security     CookieAuth
// @Param        limit   query    integer  false  "Page size (default 20, max 100)"  example(20)
// @Param        cursor  query    string   false  "Opaque pagination cursor"  example("eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0=")
// @Success      200  {object} ConversationListResponse  "Page of conversations"
// @Header       200  {string} X-Request-ID              "Echoed request id"
// @Failure      400  {object} ErrorResponse  "Invalid limit or cursor"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/conversations [get]
func (h *ConversationHandler) List(w http.ResponseWriter, r *http.Request) {
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
	res, err := h.convs.List(r.Context(), convsvc.ListParams{UserID: uid, Cursor: cursor, Limit: limit})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderConversationList(r.Context(), uid, res.Conversations)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, ConversationListResponse{
		Data: rendered, Total: res.Total, NextCursor: res.NextCursor, HasMore: res.HasMore,
	})
}

// Create handles both direct and group creation.
//
// @Summary      Create a conversation
// @Description  Creates a direct (`type=direct`, exactly 1 entry in `member_ids`) or group (`type=group`, 1-24 entries in `member_ids` + `name` required). Direct creation deduplicates: if a direct between the same pair already exists, the existing row is returned with 201.
// @Tags         conversations
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        request  body     CreateConversationRequest  true  "Creation payload"
// @Success      201      {object} ConversationResponse        "Created (or returned existing direct on dedupe — same status either way)"
// @Header       201      {string} X-Request-ID                "Echoed request id"
// @Failure      400      {object} ErrorResponse               "Malformed JSON / empty body"
// @Failure      401      {object} ErrorResponse               "Not authenticated"
// @Failure      404      {object} ErrorResponse               "A target user doesn't exist"
// @Failure      409      {object} ErrorResponse               "Concurrent direct creation conflict"
// @Failure      413      {object} ErrorResponse               "Request body too large"
// @Failure      422      {object} ErrorResponse               "Validation failed"
// @Failure      429      {object} ErrorResponse               "Rate limited"
// @Failure      500      {object} ErrorResponse               "Internal error"
// @Router       /v1/conversations [post]
func (h *ConversationHandler) Create(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var req CreateConversationRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	res, err := h.convs.Create(r.Context(), convsvc.CreateParams{
		Type:      domain.ConversationType(req.Type),
		Creator:   uid,
		MemberIDs: req.MemberIDs,
		Name:      req.Name,
		AvatarURL: req.AvatarURL,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderOne(r.Context(), uid, res.Conversation, res.Members)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, rendered)
}

// Get returns a single conversation with its members. Non-members get
// 404 (no enumeration leak).
//
// @Summary      Get conversation
// @Description  Returns the conversation row with its full member list. Caller must be a member; non-members surface as 404 so the existence of the conversation isn't leaked across the friend graph.
// @Tags         conversations
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      200  {object} ConversationResponse  "Conversation with member list"
// @Header       200  {string} X-Request-ID          "Echoed request id"
// @Failure      400  {object} ErrorResponse  "Malformed id"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      404  {object} ErrorResponse  "Conversation not found or caller is not a member"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/conversations/{id} [get]
func (h *ConversationHandler) Get(w http.ResponseWriter, r *http.Request) {
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
	res, err := h.convs.Get(r.Context(), uid, id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderOne(r.Context(), uid, res.Conversation, res.Members)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, rendered)
}

// Update patches a group conversation's name / avatar_url.
//
// @Summary      Update conversation (group only)
// @Description  Patches name / avatar_url. Caller must be an admin of the conversation. Direct conversations are immutable. Non-members get 404 — no enumeration.
// @Tags         conversations
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string                       true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     UpdateConversationRequest    true  "Profile patch"
// @Success      200      {object} ConversationResponse        "Updated conversation"
// @Header       200      {string} X-Request-ID                "Echoed request id"
// @Failure      400      {object} ErrorResponse               "Malformed JSON / id"
// @Failure      401      {object} ErrorResponse               "Not authenticated"
// @Failure      403      {object} ErrorResponse               "Caller is not an admin of the group, or conversation is direct"
// @Failure      404      {object} ErrorResponse               "Conversation not found or caller is not a member"
// @Failure      413      {object} ErrorResponse               "Request body too large"
// @Failure      422      {object} ErrorResponse               "Validation failed"
// @Failure      429      {object} ErrorResponse               "Rate limited"
// @Failure      500      {object} ErrorResponse               "Internal error"
// @Router       /v1/conversations/{id} [patch]
func (h *ConversationHandler) Update(w http.ResponseWriter, r *http.Request) {
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
	var req UpdateConversationRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	conv, err := h.convs.Update(r.Context(), convsvc.UpdateParams{
		Actor: uid, ConvID: id, Name: req.Name, AvatarURL: req.AvatarURL,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	// Re-load members so the response stays a complete ConversationResponse.
	res, err := h.convs.Get(r.Context(), uid, id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderOne(r.Context(), uid, conv, res.Members)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, rendered)
}

// Leave removes the caller's membership.
//
// @Summary      Leave a conversation
// @Description  Removes the caller's membership. For groups this is "leave"; for directs it acts as "hide" — the other party still sees the conversation.
// @Tags         conversations
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed id"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      404  {object}  ErrorResponse "Conversation not found or caller is not a member"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/conversations/{id} [delete]
func (h *ConversationHandler) Leave(w http.ResponseWriter, r *http.Request) {
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
	if err := h.convs.Leave(r.Context(), uid, id); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// SetMute toggles per-member push suppression for a conversation.
//
// @Summary      Mute / unmute a conversation
// @Description  Per-member toggle. Body: `{ "until": ISO8601 timestamp | null }`. `null` unmutes; a future timestamp suppresses pushes until then; "forever" is just a far-future stamp like `2099-01-01T00:00:00Z`. WS events still fire — only the push-fanout is gated. Non-members get 404.
// @Tags         conversations
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string                  true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     SetMuteRequest          true  "Mute deadline"
// @Success      200      {object} ConversationMemberResponse "Updated member row"
// @Header       200      {string} X-Request-ID            "Echoed request id"
// @Failure      400      {object} ErrorResponse           "Malformed JSON / id"
// @Failure      401      {object} ErrorResponse           "Not authenticated"
// @Failure      404      {object} ErrorResponse           "Conversation not found or caller not a member"
// @Failure      413      {object} ErrorResponse           "Request body too large"
// @Failure      422      {object} ErrorResponse           "Validation failed"
// @Failure      429      {object} ErrorResponse           "Rate limited"
// @Failure      500      {object} ErrorResponse           "Internal error"
// @Router       /v1/conversations/{id}/mute [patch]
func (h *ConversationHandler) SetMute(w http.ResponseWriter, r *http.Request) {
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
	var req SetMuteRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	updated, err := h.convs.SetMute(r.Context(), uid, id, req.Until)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toConversationMemberResponse(updated))
}

// SetPin toggles whether the conversation is pinned to the top of the
// caller's list.
//
// @Summary      Pin / unpin a conversation
// @Description  Per-member toggle. Body: `{ "pinned": bool }`. The server stamps `pinned_at = now()` when true, NULL when false. Pinning is a UI-ordering hint; the conversation list response includes `pinned_at` so clients can sort pinned-first. Non-members get 404.
// @Tags         conversations
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string                     true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     SetPinRequest              true  "Pin toggle"
// @Success      200      {object} ConversationMemberResponse "Updated member row"
// @Header       200      {string} X-Request-ID               "Echoed request id"
// @Failure      400      {object} ErrorResponse              "Malformed JSON / id"
// @Failure      401      {object} ErrorResponse              "Not authenticated"
// @Failure      404      {object} ErrorResponse              "Conversation not found or caller not a member"
// @Failure      413      {object} ErrorResponse              "Request body too large"
// @Failure      422      {object} ErrorResponse              "Validation failed"
// @Failure      429      {object} ErrorResponse              "Rate limited"
// @Failure      500      {object} ErrorResponse              "Internal error"
// @Router       /v1/conversations/{id}/pin [patch]
func (h *ConversationHandler) SetPin(w http.ResponseWriter, r *http.Request) {
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
	var req SetPinRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	updated, err := h.convs.SetPin(r.Context(), uid, id, *req.Pinned)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toConversationMemberResponse(updated))
}

// AddMembers adds users to a group. Admin-only.
//
// @Summary      Add members to a group
// @Description  Admin-only. Each invitee is checked against the §4.6 cap-25; the request errors with 409 the moment the cap is hit. Direct conversations don't accept members.
// @Tags         conversations
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string             true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     AddMembersRequest  true  "User ids to add"
// @Success      200      {object} AddMembersResponse "Added member rows"
// @Header       200      {string} X-Request-ID       "Echoed request id"
// @Failure      400      {object} ErrorResponse      "Malformed JSON / id"
// @Failure      401      {object} ErrorResponse      "Not authenticated"
// @Failure      403      {object} ErrorResponse      "Caller is not an admin or conversation is direct"
// @Failure      404      {object} ErrorResponse      "Conversation not found, caller not a member, or target user missing"
// @Failure      409      {object} ErrorResponse      "Group is at the 25-member cap"
// @Failure      413      {object} ErrorResponse      "Request body too large"
// @Failure      422      {object} ErrorResponse      "Validation failed"
// @Failure      429      {object} ErrorResponse      "Rate limited"
// @Failure      500      {object} ErrorResponse      "Internal error"
// @Router       /v1/conversations/{id}/members [post]
func (h *ConversationHandler) AddMembers(w http.ResponseWriter, r *http.Request) {
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
	var req AddMembersRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	res, err := h.convs.AddMembers(r.Context(), convsvc.AddMembersParams{
		Actor: uid, ConvID: id, UserIDs: req.UserIDs,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	rendered, err := h.renderMembers(r.Context(), res.Added)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, AddMembersResponse{Added: rendered})
}

// RemoveMember removes a member. Self-removal == Leave; otherwise admin-only.
//
// @Summary      Remove a member
// @Description  Removes a member from the conversation. Self-removal is always allowed (acts as Leave). Removing another user requires admin role on a group.
// @Tags         conversations
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string  true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        user_id  path     string  true  "User id to remove (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed id or user_id"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      403  {object}  ErrorResponse "Caller is not an admin (when removing another user) or conversation is direct"
// @Failure      404  {object}  ErrorResponse "Conversation not found, caller not a member, or target not a member"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/conversations/{id}/members/{user_id} [delete]
func (h *ConversationHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
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
	target, err := uuid.Parse(chi.URLParam(r, "user_id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("user_id must be a valid UUID"))
		return
	}
	if err := h.convs.RemoveMember(r.Context(), uid, id, target); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// MarkRead stamps the caller's read pointer on the conversation.
//
// @Summary      Mark conversation as read
// @Description  Sets the caller's `last_read_message_id` pointer for the conversation. Frontend uses this to compute unread counts.
// @Tags         conversations
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string           true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     MarkReadRequest  true  "Last-read message id"
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed id or JSON"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      404  {object}  ErrorResponse "Conversation not found or caller is not a member"
// @Failure      413  {object}  ErrorResponse "Request body too large"
// @Failure      422  {object}  ErrorResponse "Validation failed"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/conversations/{id}/read [post]
func (h *ConversationHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
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
	var req MarkReadRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	if err := h.convs.MarkRead(r.Context(), uid, id, req.UpToMessageID); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// --- rendering helpers --------------------------------------------------

// renderOne enriches a single conversation with its member-user join.
// callerID is needed so the response can surface the caller's mute /
// pin state at the top level.
func (h *ConversationHandler) renderOne(ctx context.Context, callerID uuid.UUID, conv domain.Conversation, members []domain.ConversationMember) (ConversationResponse, error) {
	usersByID, err := h.loadUsersForMembers(ctx, members)
	if err != nil {
		return ConversationResponse{}, err
	}
	ids := []uuid.UUID{conv.ID}
	counts := h.unreadCounts(ctx, callerID, ids)
	latest := h.latestMessages(ctx, ids)
	var lm *domain.Message
	if m, ok := latest[conv.ID]; ok {
		lm = &m
	}
	return toConversationResponse(conv, callerID, members, usersByID, h.presign, counts[conv.ID], lm), nil
}

// unreadCounts returns the per-conversation unread count for callerID,
// keyed by conversation ID. Best-effort: returns nil (→ all zeros) when
// no counter is wired in or the query fails — never propagates the
// error so a failed count can't break the conversations list.
func (h *ConversationHandler) unreadCounts(ctx context.Context, callerID uuid.UUID, convIDs []uuid.UUID) map[uuid.UUID]int64 {
	if h.msgs == nil || len(convIDs) == 0 {
		return nil
	}
	counts, err := h.msgs.CountUnreadByConversation(ctx, callerID, convIDs)
	if err != nil {
		slog.WarnContext(ctx, "conversation: unread count failed",
			slog.String("user_id", callerID.String()),
			slog.Any("err", err),
		)
		return nil
	}
	return counts
}

// latestMessages returns the most recent message per conversation, keyed
// by conversation ID. Best-effort like unreadCounts — a failure just
// means `last_message` is null on those rows, never a failed request.
func (h *ConversationHandler) latestMessages(ctx context.Context, convIDs []uuid.UUID) map[uuid.UUID]domain.Message {
	if h.msgs == nil || len(convIDs) == 0 {
		return nil
	}
	latest, err := h.msgs.LatestMessageByConversation(ctx, convIDs)
	if err != nil {
		slog.WarnContext(ctx, "conversation: latest message lookup failed",
			slog.Any("err", err),
		)
		return nil
	}
	return latest
}

// renderConversationList batch-loads members + their user records for
// every conversation in the page in TWO total round-trips
// (ListMembersForConversations + ListByIDs), regardless of page size.
//
// Caller is responsible for ensuring the actor is a member of every
// conversation in `convs` — `Service.List` enforces that already, so
// this path skips the per-row membership check.
func (h *ConversationHandler) renderConversationList(ctx context.Context, callerID uuid.UUID, convs []domain.Conversation) ([]ConversationResponse, error) {
	if len(convs) == 0 {
		return []ConversationResponse{}, nil
	}
	convIDs := make([]uuid.UUID, 0, len(convs))
	for _, c := range convs {
		convIDs = append(convIDs, c.ID)
	}
	membersByConv, err := h.convs.ListMembersForConversations(ctx, convIDs)
	if err != nil {
		return nil, err
	}
	// Collect every distinct user_id across all members, then ListByIDs
	// once for the whole page.
	userIDSet := make(map[uuid.UUID]struct{})
	for _, ms := range membersByConv {
		for _, m := range ms {
			userIDSet[m.UserID] = struct{}{}
		}
	}
	userIDs := make([]uuid.UUID, 0, len(userIDSet))
	for id := range userIDSet {
		userIDs = append(userIDs, id)
	}
	users, err := h.users.ListByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	usersByID := make(map[uuid.UUID]domain.User, len(users))
	for _, u := range users {
		usersByID[u.ID] = u
	}

	counts := h.unreadCounts(ctx, callerID, convIDs)
	latest := h.latestMessages(ctx, convIDs)
	out := make([]ConversationResponse, 0, len(convs))
	for _, c := range convs {
		var lm *domain.Message
		if m, ok := latest[c.ID]; ok {
			lm = &m
		}
		out = append(out, toConversationResponse(c, callerID, membersByConv[c.ID], usersByID, h.presign, counts[c.ID], lm))
	}
	return out, nil
}

// renderMembers builds member-row DTOs for an arbitrary slice of
// conversation_members. Used by AddMembers.
func (h *ConversationHandler) renderMembers(ctx context.Context, members []domain.ConversationMember) ([]ConversationMemberRow, error) {
	usersByID, err := h.loadUsersForMembers(ctx, members)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationMemberRow, 0, len(members))
	for _, m := range members {
		u, ok := usersByID[m.UserID]
		if !ok {
			u = domain.User{ID: m.UserID}
		}
		out = append(out, toConversationMemberRow(m, u, h.presign))
	}
	return out, nil
}

// loadUsersForMembers batch-loads the user records for every member in
// the slice, returning a map keyed by user_id.
func (h *ConversationHandler) loadUsersForMembers(ctx context.Context, members []domain.ConversationMember) (map[uuid.UUID]domain.User, error) {
	if len(members) == 0 {
		return map[uuid.UUID]domain.User{}, nil
	}
	ids := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	users, err := h.users.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]domain.User, len(users))
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}
