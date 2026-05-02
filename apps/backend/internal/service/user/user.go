// Package user is the user-profile service: UpdateProfile, UploadAvatar,
// SoftDeleteAccount. Composes the user repository (§3.1) and the
// objectstore wrapper (§2.7). Validation of length/charset is the
// handler's job (validator/v10 tags); the service trusts shape and only
// enforces invariants the DB couldn't (e.g. server-side MIME detection
// per §9.2).
package user

import (
	"bytes"
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
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
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

// GetByID returns the public profile for id. Returns apierror.NotFound
// when the user doesn't exist or is soft-deleted.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return domain.User{}, apierror.NotFound("user")
		}
		return domain.User{}, apierror.Internal("get user").WithCause(err)
	}
	return u, nil
}

// SearchParams is the input to Search.
type SearchParams struct {
	Query  string             // q query param; "" returns all users
	Cursor *pagination.Cursor // nil for first page
	Limit  int                // 0 → DefaultLimit; clamped to MaxLimit
}

// SearchResult is the paginated payload returned by Search.
type SearchResult struct {
	Users      []domain.User
	NextCursor *string
	HasMore    bool
}

// Search returns up to limit users whose username/display_name match q
// (case-insensitive prefix; trigram). Empty q returns the catalog in
// (created_at DESC, id DESC) order. The pagination envelope is the §6.4
// keyset shape — never offset.
func (s *Service) Search(ctx context.Context, p SearchParams) (SearchResult, error) {
	overFetched, err := s.users.ListByPrefix(ctx, p.Query, p.Cursor, p.Limit)
	if err != nil {
		return SearchResult{}, apierror.Internal("search users").WithCause(err)
	}
	data, next, hasMore := pagination.Page(overFetched, p.Limit, func(u domain.User) pagination.Cursor {
		return pagination.Cursor{Timestamp: u.CreatedAt, ID: u.ID}
	})
	return SearchResult{Users: data, NextCursor: next, HasMore: hasMore}, nil
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

	if err := s.storage.Put(ctx, key, mime, bytes.NewReader(data), int64(len(data))); err != nil {
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
