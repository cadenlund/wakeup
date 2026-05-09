package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	contactssvc "github.com/cadenlund/wakeup/apps/backend/internal/service/contacts"
)

// ContactsHandler hosts /v1/contacts/* endpoints. Composes the contacts
// service + auth so callers must have a session to hit Match.
type ContactsHandler struct {
	contacts *contactssvc.Service
	auth     *auth.Service
	v        *validator.Validate
	presign  Presigner // optional; nil → raw avatar keys
}

// NewContactsHandler wires up the handler.
func NewContactsHandler(c *contactssvc.Service, a *auth.Service, v *validator.Validate, presign Presigner) (*ContactsHandler, error) {
	if c == nil {
		return nil, errors.New("httpapi: ContactsHandler requires non-nil contacts service")
	}
	if a == nil {
		return nil, errors.New("httpapi: ContactsHandler requires non-nil auth service")
	}
	if v == nil {
		return nil, errors.New("httpapi: ContactsHandler requires non-nil validator")
	}
	return &ContactsHandler{contacts: c, auth: a, v: v, presign: presign}, nil
}

// Mount attaches /v1/contacts/* onto r.
func (h *ContactsHandler) Mount(r chi.Router) {
	r.Route("/v1/contacts", func(r chi.Router) {
		r.Post("/match", h.Match)
	})
}

// ContactsMatchRequest is the body for POST /v1/contacts/match.
type ContactsMatchRequest struct {
	// EmailHashes is a slice of lowercase hex SHA-256 strings (exactly
	// 64 chars each). Cap: 1000 entries — chunk client-side past that.
	// Length check at the validator; lowercase-hex check at the service
	// (regex `/^[0-9a-f]{64}$/`). Two passes keep the malformed-input
	// error path typed (Validation, not Internal).
	EmailHashes []string `json:"email_hashes" validate:"required,min=1,max=1000,dive,len=64" example:"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`
}

// ContactsMatchResponse is the body of POST /v1/contacts/match. Only
// matched users surface — unmatched hashes are not echoed.
type ContactsMatchResponse struct {
	Matched []UserResponse `json:"matched"`
}

// Match runs the email-hash lookup. Privacy posture: raw addresses
// never leave the client; hashes are matched server-side via binary
// equality against `users.email_hash` (a stored SHA-256 generated
// column).
//
// @Summary      Match contacts by email hash
// @Description  Privacy-preserving contacts sync. Client SHA-256s each contact email (`sha256(lower(trim(email)))`), hex-encodes, and POSTs the slice. Server hex-decodes and matches against `users.email_hash` (bytea, indexed). Unmatched hashes are not echoed and not logged. Soft-deleted users are excluded. Cap 1000 entries per request — chunk client-side past that.
// @Tags         contacts
// @Accept       json
// @Produce      json
// @Security     CookieAuth
// @Param        request  body     ContactsMatchRequest   true  "Hashes to look up"
// @Success      200      {object} ContactsMatchResponse  "Matched users (may be empty)"
// @Header       200      {string} X-Request-ID           "Echoed request id"
// @Failure      400      {object} ErrorResponse          "Malformed JSON"
// @Failure      401      {object} ErrorResponse          "Not authenticated"
// @Failure      413      {object} ErrorResponse          "Request body too large"
// @Failure      422      {object} ErrorResponse          "Validation failed"
// @Failure      429      {object} ErrorResponse          "Rate limited"
// @Failure      500      {object} ErrorResponse          "Internal error"
// @Router       /v1/contacts/match [post]
func (h *ContactsHandler) Match(w http.ResponseWriter, r *http.Request) {
	if _, err := h.auth.CurrentUser(r.Context()); err != nil {
		WriteError(w, r, err)
		return
	}
	var req ContactsMatchRequest
	if e := DecodeJSON(r, h.v, &req); e != nil {
		WriteError(w, r, e)
		return
	}
	matched, err := h.contacts.Match(r.Context(), req.EmailHashes)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	out := make([]UserResponse, 0, len(matched))
	for _, u := range matched {
		out = append(out, toUserResponse(u, h.presign))
	}
	WriteJSON(w, http.StatusOK, ContactsMatchResponse{Matched: out})
}
