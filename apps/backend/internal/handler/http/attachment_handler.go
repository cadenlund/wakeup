package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/attachment"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
)

// maxAttachmentMultipartBytes is the body cap for POST /v1/attachments
// (§9.2 step 1). 50 MiB plus 1 KiB slack for multipart framing — the
// MaxBytesReader catches a 50 MiB+1 byte file before the multipart
// parser even buffers it.
const maxAttachmentMultipartBytes = attachment.MaxAttachmentBytes + (1 << 10)

// AttachmentHandler hosts /v1/attachments POST + GET. The service does
// MIME detection / membership gating; this layer only handles the
// multipart envelope and the DTO.
type AttachmentHandler struct {
	atts *attachment.Service
	auth *auth.Service
}

// NewAttachmentHandler wires the handler.
func NewAttachmentHandler(atts *attachment.Service, a *auth.Service) (*AttachmentHandler, error) {
	if atts == nil {
		return nil, errors.New("httpapi: AttachmentHandler requires non-nil attachment service")
	}
	if a == nil {
		return nil, errors.New("httpapi: AttachmentHandler requires non-nil auth service")
	}
	return &AttachmentHandler{atts: atts, auth: a}, nil
}

// Mount attaches /v1/attachments routes onto r.
func (h *AttachmentHandler) Mount(r chi.Router) {
	r.Post("/v1/attachments", h.Upload)
	r.Get("/v1/attachments/{id}", h.Get)
}

// Upload accepts a multipart `file` field (max 50 MiB) and persists the
// uploaded bytes via the attachment service.
//
// @Summary      Upload an attachment
// @Description  Accepts a multipart form with a single `file` field. Max 50 MiB; the server detects the MIME from the first 512 bytes (§9.2) — the client-supplied content-type and filename extension are ignored for type-checking purposes. Allowed types: image/png, image/jpeg, image/gif, image/webp, application/pdf, text/plain. The response includes a 5-minute presigned URL so the uploader can render the attachment without a follow-up GET.
// @Tags         attachments
// @Accept       multipart/form-data
// @Produce      json
// @Security     CookieAuth
// @Param        file     formData file                true  "Attachment file (max 50 MiB)"
// @Success      201      {object} AttachmentResponse  "Created attachment with presigned URL"
// @Header       201      {string} X-Request-ID        "Echoed request id"
// @Failure      400      {object} ErrorResponse       "Malformed multipart / missing file"
// @Failure      401      {object} ErrorResponse       "Not authenticated"
// @Failure      413      {object} ErrorResponse       "Attachment exceeds 50 MiB cap"
// @Failure      422      {object} ErrorResponse       "Disallowed content-type or empty filename"
// @Failure      429      {object} ErrorResponse       "Rate limited"
// @Failure      500      {object} ErrorResponse       "Internal error"
// @Router       /v1/attachments [post]
func (h *AttachmentHandler) Upload(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth.CurrentUser(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}

	// §9.2 step 1: MaxBytesReader BEFORE ParseMultipartForm so an
	// attacker can't stream 50 GB past the cap and trash the host.
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentMultipartBytes)

	if err := r.ParseMultipartForm(maxAttachmentMultipartBytes); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			WriteError(w, r, apierror.PayloadTooLarge("attachment exceeds size cap"))
			return
		}
		WriteError(w, r, apierror.BadRequest("malformed multipart form"))
		return
	}
	// 50 MiB cap means parts can spill from memory to /tmp; RemoveAll
	// ensures we don't leak the spilled files on the host. Safe to defer
	// after a successful ParseMultipartForm: the form is non-nil and
	// RemoveAll is idempotent. (CodeRabbit PR #44.)
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	file, header, err := r.FormFile("file")
	if err != nil {
		WriteError(w, r, apierror.BadRequest("missing `file` form field"))
		return
	}
	defer func() { _ = file.Close() }()

	created, err := h.atts.Upload(r.Context(), attachment.UploadParams{
		UploaderID:   uid,
		Filename:     header.Filename,
		Body:         file,
		DeclaredSize: header.Size,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	url, expiresAt, err := h.atts.Presign(r.Context(), created)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, AttachmentResponse{
		ID: created.ID, URL: url, ExpiresAt: expiresAt,
		Filename: created.Filename, ContentType: created.ContentType,
		SizeBytes: created.SizeBytes,
	})
}

// Get returns the attachment metadata + a freshly-presigned URL.
//
// @Summary      Get attachment with presigned URL
// @Description  Returns metadata + a 5-minute presigned download URL. Membership-gated per §9.3 — the caller must be a member of a conversation containing a message linked to this attachment, OR the uploader of an attachment that has not yet been linked to any message. Any failure (missing or unauthorized) returns 404 — never 403, never leaks existence.
// @Tags         attachments
// @Produce      json
// @Security     CookieAuth
// @Param        id   path     string                true  "Attachment id (UUID v7)"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Success      200  {object} AttachmentResponse    "Attachment with presigned URL"
// @Header       200  {string} X-Request-ID          "Echoed request id"
// @Failure      400  {object} ErrorResponse         "Malformed id"
// @Failure      401  {object} ErrorResponse         "Not authenticated"
// @Failure      404  {object} ErrorResponse         "Attachment not found or caller not authorized (no enumeration)"
// @Failure      429  {object} ErrorResponse         "Rate limited"
// @Failure      500  {object} ErrorResponse         "Internal error"
// @Router       /v1/attachments/{id} [get]
func (h *AttachmentHandler) Get(w http.ResponseWriter, r *http.Request) {
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
	att, err := h.atts.GetForCaller(r.Context(), id, uid)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	url, expiresAt, err := h.atts.Presign(r.Context(), att)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, AttachmentResponse{
		ID: att.ID, URL: url, ExpiresAt: expiresAt,
		Filename: att.Filename, ContentType: att.ContentType,
		SizeBytes: att.SizeBytes,
	})
}
