// Package attachment is the service layer for the §6.2 / §9 attachments
// flow: server-proxied uploads with MIME detection on the first 512
// bytes, membership-gated reads (§9.3), and short-lived presigned GET
// URLs.
//
// Flow:
//
//  1. Upload reads the body (capped at MaxAttachmentBytes), sniffs the
//     MIME on the first 512 bytes, sanitizes the client-supplied
//     filename, generates a UUID-only S3 key per §9.1, writes to the
//     object store using the SERVER-detected MIME, and persists the
//     `attachments` row.
//
//  2. GetForCaller fetches the row and uses the repo's CallerCanRead
//     single-round-trip permission check. On false it returns
//     apierror.NotFound (§9.3 — never Forbidden, no enumeration).
//
//  3. Presign issues a 5-minute presigned GET with
//     response-content-disposition: attachment; filename="<sanitized>"
//     so the browser sees the original filename even though the S3 key
//     doesn't contain it.
package attachment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/objectstore"
	attrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/attachment"
)

// MaxAttachmentBytes is the §4.6 / §9.2 cap.
const MaxAttachmentBytes = 50 * 1024 * 1024 // 50 MiB

// MaxFilenameLen is the §4.6 cap for the sanitized filename stored in
// the DB (after we strip path separators / NUL / control chars).
const MaxFilenameLen = 255

// PresignTTL is the §9.3 5-minute window.
const PresignTTL = 5 * time.Minute

// allowedMIMEs is the §9.2 attachment whitelist. Common image / doc /
// text formats only — everything else gets rejected at upload time. We
// can grow this list as use cases land; conservative-by-default keeps
// novel parser bugs out of the trust boundary.
var allowedMIMEs = map[string]struct{}{
	"image/png":       {},
	"image/jpeg":      {},
	"image/gif":       {},
	"image/webp":      {},
	"application/pdf": {},
	"text/plain":      {},
}

// Service is the attachment service.
type Service struct {
	repo    *attrepo.Queries
	storage *objectstore.Store
	logger  *slog.Logger
}

// Config builds the service.
type Config struct {
	Repo    *attrepo.Queries
	Storage *objectstore.Store
	Logger  *slog.Logger
}

// New constructs the service.
func New(cfg Config) (*Service, error) {
	if cfg.Repo == nil {
		return nil, errors.New("attachment: Config.Repo is required")
	}
	if cfg.Storage == nil {
		return nil, errors.New("attachment: Config.Storage is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: cfg.Repo, storage: cfg.Storage, logger: logger}, nil
}

// UploadParams is the input to Upload.
type UploadParams struct {
	UploaderID uuid.UUID
	// Filename is the client-supplied filename from the multipart part.
	// We sanitize per §9.2 — strip path separators / NUL / control
	// chars, truncate, reject empty after sanitization.
	Filename string
	Body     io.Reader
	// DeclaredSize is the multipart-supplied content-length; -1 if not
	// known. We pre-flight against the cap when known and re-check after
	// reading the bytes either way.
	DeclaredSize int64
}

// Upload reads the body, detects MIME, sanitizes the filename, writes
// the object to storage, and persists the row.
//
// Errors map to:
//   - declared > cap → apierror.PayloadTooLarge
//   - empty body → apierror.BadRequest
//   - sanitized filename empty → apierror.Validation
//   - detected MIME not on the allowlist → apierror.Validation
//   - storage cap exceeded mid-stream → apierror.PayloadTooLarge
//   - any other DB / storage error → apierror.Internal
//
// On a DB failure AFTER the S3 object is written, we accept the orphan
// (the §9.6 sweeper will reap it). Compensating-delete on DB failure is
// possible but adds a second window where S3 succeeds and the user sees
// success — leaving the orphan is the simpler invariant.
func (s *Service) Upload(ctx context.Context, p UploadParams) (domain.Attachment, error) {
	if p.DeclaredSize > MaxAttachmentBytes {
		return domain.Attachment{}, apierror.PayloadTooLarge(
			fmt.Sprintf("attachment exceeds %d bytes", MaxAttachmentBytes),
		)
	}
	if p.Body == nil {
		return domain.Attachment{}, apierror.BadRequest("attachment body is missing")
	}

	filename := sanitizeFilename(p.Filename)
	if filename == "" {
		return domain.Attachment{}, apierror.Validation([]apierror.FieldError{{
			Field: "filename", Code: "REQUIRED",
			Message: "filename is required (after sanitization)",
		}})
	}

	// Read the entire (capped) body into memory. With a 50 MiB cap the
	// peak per-request memory is bounded — same approach as the avatar
	// service and the SDK signer needs a seekable body anyway.
	data, err := io.ReadAll(io.LimitReader(p.Body, MaxAttachmentBytes+1))
	if err != nil {
		return domain.Attachment{}, apierror.Internal("read attachment body").WithCause(err)
	}
	if int64(len(data)) > MaxAttachmentBytes {
		return domain.Attachment{}, apierror.PayloadTooLarge(
			fmt.Sprintf("attachment exceeds %d bytes", MaxAttachmentBytes),
		)
	}
	if len(data) == 0 {
		return domain.Attachment{}, apierror.BadRequest("attachment body is empty")
	}

	// Server-side MIME detection (§9.2 step 2). http.DetectContentType
	// reads up to 512 bytes; data may be shorter, that's fine.
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	mime := strings.SplitN(http.DetectContentType(sniff), ";", 2)[0]
	if _, ok := allowedMIMEs[mime]; !ok {
		return domain.Attachment{}, apierror.Validation([]apierror.FieldError{{
			Field: "content_type", Code: "INVALID_FORMAT",
			Message: fmt.Sprintf("attachment content type %q is not allowed", mime),
		}})
	}

	// Generate UUID-keyed S3 path per §9.1. Original filename never
	// enters the key — sanitized filename is stored only in the DB row
	// and surfaced via response-content-disposition on presigned GET.
	attID, err := uuid.NewV7()
	if err != nil {
		return domain.Attachment{}, apierror.Internal("uuid").WithCause(err)
	}
	key := fmt.Sprintf("attachments/%s", attID)

	if err := s.storage.Put(ctx, key, mime, bytes.NewReader(data), int64(len(data))); err != nil {
		if errors.Is(err, objectstore.ErrPayloadTooLarge) {
			return domain.Attachment{}, apierror.PayloadTooLarge(
				fmt.Sprintf("attachment exceeds %d bytes", MaxAttachmentBytes),
			)
		}
		return domain.Attachment{}, apierror.Internal("store attachment").WithCause(err)
	}

	row, err := s.repo.Create(ctx, attrepo.CreateParams{
		ID:          attID,
		UploaderID:  p.UploaderID,
		StorageKey:  key,
		Filename:    filename,
		ContentType: mime,
		SizeBytes:   int64(len(data)),
	})
	if err != nil {
		// S3 wrote, DB didn't. The §9.6 orphan sweeper will reap the
		// object in the next 24h tick. Log so we can spot a chronic DB
		// outage that would push the orphan rate up.
		s.logger.Warn("attachment: row insert failed after S3 put",
			slog.String("key", key),
			slog.String("error", err.Error()),
		)
		return domain.Attachment{}, apierror.Internal("persist attachment").WithCause(err)
	}
	return row, nil
}

// GetForCaller returns the attachment iff the caller can read it per
// §9.3. On non-existence OR permission denial, returns
// apierror.NotFound — never Forbidden, never leaks existence.
func (s *Service) GetForCaller(ctx context.Context, attID, userID uuid.UUID) (domain.Attachment, error) {
	canRead, err := s.repo.CallerCanRead(ctx, attID, userID)
	if err != nil {
		return domain.Attachment{}, apierror.Internal("attachment caller can read").WithCause(err)
	}
	if !canRead {
		return domain.Attachment{}, apierror.NotFound("attachment")
	}
	row, err := s.repo.GetByID(ctx, attID)
	if err != nil {
		// canRead=true with GetByID NotFound would be a tight race
		// (sweeper deleted it between the two queries). Fall through
		// as NotFound from the caller's perspective.
		if errors.Is(err, attrepo.ErrNotFound) {
			return domain.Attachment{}, apierror.NotFound("attachment")
		}
		return domain.Attachment{}, apierror.Internal("get attachment").WithCause(err)
	}
	return row, nil
}

// Presign issues a 5-minute presigned GET URL with
// response-content-disposition: attachment; filename="<sanitized>". The
// returned `expiresAt` is the absolute time the URL stops working, used
// by the DTO so the client knows when to refetch.
func (s *Service) Presign(ctx context.Context, att domain.Attachment) (string, time.Time, error) {
	disposition := fmt.Sprintf(`attachment; filename=%q`, att.Filename)
	url, err := s.storage.PresignGet(ctx, att.StorageKey, PresignTTL, disposition)
	if err != nil {
		return "", time.Time{}, apierror.Internal("presign attachment").WithCause(err)
	}
	return url, time.Now().Add(PresignTTL), nil
}

// sanitizeFilename strips path separators / NUL / control chars and
// truncates to MaxFilenameLen runes. Returns empty if nothing useful
// remains.
//
// We do NOT do unicode-NFC normalization or homoglyph filtering — the
// sanitized name is for display only (the S3 key never sees it), so
// defensive equivalence isn't a security need.
func sanitizeFilename(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return ""
	}
	out := make([]rune, 0, len(in))
	for _, r := range in {
		switch {
		case r == '/' || r == '\\' || r == 0x00:
			continue
		case r < 0x20 || r == 0x7f:
			continue
		default:
			out = append(out, r)
		}
	}
	if len(out) > MaxFilenameLen {
		out = out[:MaxFilenameLen]
	}
	// Re-trim — leading/trailing spaces could remain after stripping.
	cleaned := strings.TrimSpace(string(out))
	if !utf8.ValidString(cleaned) {
		return ""
	}
	return cleaned
}
