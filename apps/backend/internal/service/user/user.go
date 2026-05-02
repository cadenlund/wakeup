// Package user is the user-profile service: UpdateProfile, UploadAvatar,
// SoftDeleteAccount. Composes the user repository (§3.1) and the
// objectstore wrapper (§2.7). Validation of length/charset is the
// handler's job (validator/v10 tags); the service trusts shape and only
// enforces invariants the DB couldn't (e.g. server-side MIME detection
// per §9.2).
package user

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/objectstore"
	repo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
)

// MaxAvatarBytes is the §4.6 cap. The handler MUST also wrap the request
// body in http.MaxBytesReader before reading multipart so an attacker
// can't stream past this — the cap here is defense-in-depth against
// callers that bypass the handler check.
const MaxAvatarBytes = 5 * 1024 * 1024 // 5 MiB

// allowedAvatarMIMEs is the §9.5 / §9.2 whitelist for avatar uploads.
// Server-side MIME detection MUST resolve to one of these — the
// multipart-supplied Content-Type is not trusted.
var allowedAvatarMIMEs = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// Service is the user-profile service.
type Service struct {
	users   *repo.Queries
	storage *objectstore.Store
}

// Config builds the service.
type Config struct {
	Users   *repo.Queries
	Storage *objectstore.Store
}

// New constructs the service. Returns an error if any dependency is missing.
func New(cfg Config) (*Service, error) {
	if cfg.Users == nil {
		return nil, errors.New("user: Config.Users is required")
	}
	if cfg.Storage == nil {
		return nil, errors.New("user: Config.Storage is required")
	}
	return &Service{users: cfg.Users, storage: cfg.Storage}, nil
}

// UpdateProfileParams is the input to UpdateProfile. Each pointer field
// uses nil-means-unchanged semantics (matches the repo's COALESCE pattern).
type UpdateProfileParams struct {
	UserID      uuid.UUID
	DisplayName *string
	AvatarURL   *string
	ColorScheme *string
}

// UpdateProfile patches the user's writable profile fields.
//
// ColorScheme is validated against the §4.6 enum (light / dark / system)
// even though the DB CHECK constraint also catches it — a service-layer
// reject is faster + gives a typed apierror.Validation.
func (s *Service) UpdateProfile(ctx context.Context, p UpdateProfileParams) (domain.User, error) {
	if p.ColorScheme != nil {
		switch *p.ColorScheme {
		case "light", "dark", "system":
			// ok
		default:
			return domain.User{}, apierror.Validation([]apierror.FieldError{{
				Field: "color_scheme", Code: "INVALID_VALUE",
				Message: "color_scheme must be one of: light, dark, system",
			}})
		}
	}

	updated, err := s.users.Update(ctx, repo.UpdateParams{
		ID:          p.UserID,
		DisplayName: p.DisplayName,
		AvatarURL:   p.AvatarURL,
		ColorScheme: p.ColorScheme,
	})
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return domain.User{}, apierror.NotFound("user")
		}
		return domain.User{}, apierror.Internal("update profile").WithCause(err)
	}
	return updated, nil
}

// UploadAvatar reads the avatar bytes from body, validates the MIME via
// server-side detection on the first 512 bytes, writes to objectstore at
// `avatars/{user_id}/{uuid}.{ext}`, and updates the user row's avatar_url
// to the storage key (the handler/DTO maps the key to a public URL —
// §9.5.a is still open).
//
// Caps:
//   - size > MaxAvatarBytes → apierror.PayloadTooLarge (the handler should
//     have already enforced this via http.MaxBytesReader; we double-check).
//   - detected MIME not in allowedAvatarMIMEs → apierror.Validation.
//
// On success returns the updated user with the new avatar_url populated.
func (s *Service) UploadAvatar(ctx context.Context, userID uuid.UUID, body io.Reader, declaredSize int64) (domain.User, error) {
	if declaredSize > MaxAvatarBytes {
		return domain.User{}, apierror.PayloadTooLarge(
			fmt.Sprintf("avatar exceeds %d bytes", MaxAvatarBytes),
		)
	}
	if body == nil {
		return domain.User{}, apierror.BadRequest("avatar body is missing")
	}

	// Read the entire (capped) body into memory. With a 5 MiB cap this is
	// fine — and the objectstore Put already buffers anyway. We need the
	// full bytes locally so we can sniff MIME *and* hand a seekable Reader
	// to the SDK without reading twice.
	data, err := io.ReadAll(io.LimitReader(body, MaxAvatarBytes+1))
	if err != nil {
		return domain.User{}, apierror.Internal("read avatar body").WithCause(err)
	}
	if int64(len(data)) > MaxAvatarBytes {
		return domain.User{}, apierror.PayloadTooLarge(
			fmt.Sprintf("avatar exceeds %d bytes", MaxAvatarBytes),
		)
	}
	if len(data) == 0 {
		return domain.User{}, apierror.BadRequest("avatar body is empty")
	}

	// Server-side MIME detection. http.DetectContentType reads up to 512
	// bytes; data may be shorter, that's fine.
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	mime := strings.SplitN(http.DetectContentType(sniff), ";", 2)[0]
	ext, ok := allowedAvatarMIMEs[mime]
	if !ok {
		return domain.User{}, apierror.Validation([]apierror.FieldError{{
			Field: "file", Code: "INVALID_FORMAT",
			Message: fmt.Sprintf("avatar must be image/png, image/jpeg, image/gif, or image/webp (got %s)", mime),
		}})
	}

	// Generate UUID-keyed S3 path per §9.1 — original filename never enters
	// the key, so user-supplied paths can't traverse / inject.
	objID, err := uuid.NewV7()
	if err != nil {
		return domain.User{}, apierror.Internal("uuid").WithCause(err)
	}
	key := fmt.Sprintf("avatars/%s/%s%s", userID, objID, ext)

	if err := s.storage.Put(ctx, key, mime, byteReader(data), int64(len(data))); err != nil {
		if errors.Is(err, objectstore.ErrPayloadTooLarge) {
			return domain.User{}, apierror.PayloadTooLarge(
				fmt.Sprintf("avatar exceeds %d bytes", MaxAvatarBytes),
			)
		}
		return domain.User{}, apierror.Internal("store avatar").WithCause(err)
	}

	// Persist the storage key as avatar_url. The DTO converter at the
	// handler layer (milestone 3.7) will compose the public URL from a
	// configured CDN base (§9.5.a).
	updated, err := s.users.Update(ctx, repo.UpdateParams{
		ID:        userID,
		AvatarURL: &key,
	})
	if err != nil {
		// We've already written to S3. The orphan object is acceptable
		// for v1 — on user deletion or reupload it gets replaced. A more
		// rigorous flow would compensating-delete on DB failure.
		if errors.Is(err, repo.ErrNotFound) {
			return domain.User{}, apierror.NotFound("user")
		}
		return domain.User{}, apierror.Internal("update avatar_url").WithCause(err)
	}
	return updated, nil
}

// SoftDeleteAccount soft-deletes the user. Per §4.6, content stays — the
// row's deleted_at gets set and the user becomes invisible to lists/login.
func (s *Service) SoftDeleteAccount(ctx context.Context, userID uuid.UUID) error {
	if err := s.users.SoftDelete(ctx, userID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return apierror.NotFound("user")
		}
		return apierror.Internal("soft delete").WithCause(err)
	}
	return nil
}

// byteReader wraps a []byte in an io.Reader. Could be a bytes.NewReader
// directly; this thin alias avoids importing bytes here just for one type.
func byteReader(b []byte) io.Reader { return &simpleReader{b: b} }

type simpleReader struct {
	b   []byte
	pos int
}

func (r *simpleReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}
