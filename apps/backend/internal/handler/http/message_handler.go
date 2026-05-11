package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	msgsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/message"
)

// MessageHandler hosts every /v1/messages/* endpoint plus the per-
// conversation messages collection routes
// (/v1/conversations/{id}/messages). Wraps the message service.
type MessageHandler struct {
	msgs *msgsvc.Service
	auth *auth.Service
	v    *validator.Validate
}

// NewMessageHandler wires up the handler.
func NewMessageHandler(msgs *msgsvc.Service, a *auth.Service, v *validator.Validate) (*MessageHandler, error) {
	if msgs == nil {
		return nil, errors.New("httpapi: MessageHandler requires non-nil message service")
	}
	if a == nil {
		return nil, errors.New("httpapi: MessageHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: MessageHandler requires non-nil validator")
	}
	return &MessageHandler{msgs: msgs, auth: a, v: v}, nil
}

// Mount attaches all message routes onto r.
func (h *MessageHandler) Mount(r chi.Router) {
	r.Get("/v1/conversations/{id}/messages", h.List)
	r.Post("/v1/conversations/{id}/messages", h.Send)
	r.Patch("/v1/messages/{id}", h.Edit)
	r.Delete("/v1/messages/{id}", h.Delete)
	r.Get("/v1/messages/{id}/reads", h.ListReads)
}

// List returns a page of messages in the conversation, newest-first.
//
// @Summary      List messages in a conversation
// @Description  Returns a page of messages in the conversation keyset-paginated by `created_at DESC, id DESC` per §6.4. Soft-deleted rows are included so the §4.6 placeholder can render — `body` is blanked and `is_deleted=true` so the client can render "this message was deleted" without leaking the original text.
// @Tags         messages
// @Produce      json
// @Security     CookieAuth
// @Param        id      path     string   true   "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        limit   query    integer  false  "Page size (default 20, max 100)"  example(20)
// @Param        cursor  query    string   false  "Opaque pagination cursor"  example("eyJpZCI6IjAxOTJmNWEzLTdjMWItN2EzZi05YjFjLTJkM2U0ZjVhNmI3YyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0=")
// @Param        q       query    string   false  "Full-text search query (Postgres `plainto_tsquery('english', q)`)"  example("hello")
// @Success      200  {object} MessageListResponse  "Page of messages"
// @Header       200  {string} X-Request-ID         "Echoed request id"
// @Failure      400  {object} ErrorResponse  "Malformed id, limit, or cursor"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      404  {object} ErrorResponse  "Conversation not found or caller is not a member"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/conversations/{id}/messages [get]
func (h *MessageHandler) List(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	cid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
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
	res, err := h.msgs.List(r.Context(), msgsvc.ListParams{
		Actor: uid, ConversationID: cid, Cursor: cursor, Limit: limit,
		Query: r.URL.Query().Get("q"),
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, MessageListResponse{
		Data: toMessageList(res.Messages), Total: res.Total, NextCursor: res.NextCursor, HasMore: res.HasMore,
	})
}

// Send creates a new message in the conversation.
//
// @Summary      Send a message
// @Description  Creates a new message in the conversation. The caller must be a member. `attachment_ids` link previously-uploaded attachments; `reply_to_message_id` must live in the same conversation (cross-conversation replies are rejected at 422). On success, fans out a `message.new` event on `conv:<id>:messages` per §4.5 / §7.2.
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string                true  "Conversation id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     SendMessageRequest    true  "Message payload"
// @Success      201      {object} MessageResponse       "Created message"
// @Header       201      {string} X-Request-ID          "Echoed request id"
// @Failure      400      {object} ErrorResponse         "Malformed JSON / id"
// @Failure      401      {object} ErrorResponse         "Not authenticated"
// @Failure      404      {object} ErrorResponse         "Conversation not found or caller is not a member"
// @Failure      413      {object} ErrorResponse         "Request body too large"
// @Failure      422      {object} ErrorResponse         "Validation failed (empty/overlong body, cross-conv reply, missing reply target)"
// @Failure      429      {object} ErrorResponse         "Rate limited"
// @Failure      500      {object} ErrorResponse         "Internal error"
// @Router       /v1/conversations/{id}/messages [post]
func (h *MessageHandler) Send(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	cid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	var req SendMessageRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	res, err := h.msgs.Send(r.Context(), msgsvc.SendParams{
		ConversationID:   cid,
		Sender:           uid,
		Body:             req.Body,
		AttachmentIDs:    req.AttachmentIDs,
		ReplyToMessageID: req.ReplyToMessageID,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, toMessageResponse(res.Message))
}

// Edit updates the body of an existing message. Owner-only.
//
// @Summary      Edit a message
// @Description  Updates the body of an existing message. Caller must be the original sender; otherwise 403. Refuses on already-deleted rows (404). On success, stamps `edited_at` and fans out a `message.edited` event.
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        id       path     string                true  "Message id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request  body     EditMessageRequest    true  "Edit payload"
// @Success      200      {object} MessageResponse       "Updated message"
// @Header       200      {string} X-Request-ID          "Echoed request id"
// @Failure      400      {object} ErrorResponse         "Malformed JSON / id"
// @Failure      401      {object} ErrorResponse         "Not authenticated"
// @Failure      403      {object} ErrorResponse         "Caller is not the message's sender"
// @Failure      404      {object} ErrorResponse         "Message not found or already deleted"
// @Failure      413      {object} ErrorResponse         "Request body too large"
// @Failure      422      {object} ErrorResponse         "Validation failed"
// @Failure      429      {object} ErrorResponse         "Rate limited"
// @Failure      500      {object} ErrorResponse         "Internal error"
// @Router       /v1/messages/{id} [patch]
func (h *MessageHandler) Edit(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	mid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	var req EditMessageRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	updated, err := h.msgs.Edit(r.Context(), msgsvc.EditParams{
		Actor: uid, MessageID: mid, Body: req.Body,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, toMessageResponse(updated))
}

// Delete soft-deletes a message. Sender or conversation admin.
//
// @Summary      Delete a message
// @Description  Soft-deletes a message. Allowed for the original sender or for an admin of the conversation. The row stays in history with `is_deleted=true` and `body` blanked at the wire (§4.6 placeholder rendering). Idempotent: re-deleting an already-deleted message returns 204.
// @Tags         messages
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Message id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      204  "No Content"
// @Header       204  {string}  X-Request-ID  "Echoed request id"
// @Failure      400  {object}  ErrorResponse "Malformed id"
// @Failure      401  {object}  ErrorResponse "Not authenticated"
// @Failure      403  {object}  ErrorResponse "Caller is neither the sender nor a conversation admin"
// @Failure      404  {object}  ErrorResponse "Message not found"
// @Failure      429  {object}  ErrorResponse "Rate limited"
// @Failure      500  {object}  ErrorResponse "Internal error"
// @Router       /v1/messages/{id} [delete]
func (h *MessageHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	mid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	if err := h.msgs.Delete(r.Context(), uid, mid); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteNoContent(w)
}

// ListReads returns the read receipts for a message.
//
// @Summary      List read receipts for a message
// @Description  Returns who has marked the message as read, newest read first. Caller must be a member of the message's conversation; otherwise 404.
// @Tags         messages
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string  true  "Message id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      200  {object} MessageReadsResponse  "Read receipts"
// @Header       200  {string} X-Request-ID          "Echoed request id"
// @Failure      400  {object} ErrorResponse  "Malformed id"
// @Failure      401  {object} ErrorResponse  "Not authenticated"
// @Failure      404  {object} ErrorResponse  "Message not found or caller not a member"
// @Failure      429  {object} ErrorResponse  "Rate limited"
// @Failure      500  {object} ErrorResponse  "Internal error"
// @Router       /v1/messages/{id}/reads [get]
func (h *MessageHandler) ListReads(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	mid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, apierror.BadRequest("id must be a valid UUID"))
		return
	}
	reads, err := h.msgs.ListReads(r.Context(), uid, mid)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, MessageReadsResponse{Data: toMessageReadList(reads)})
}
