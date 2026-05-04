// Package contacts implements POST /v1/contacts/match — the §6.2
// privacy-preserving "which of my address book is on Wakeup" endpoint.
//
// Wire shape: client SHA-256s each contact email (lowercased, trimmed),
// hex-encodes, and POSTs the slice. Server hex-decodes and matches
// against `users.email_hash` (the bytea generated column from migration
// 0001) — the raw email never leaves the device.
package contacts

import (
	"context"
	"errors"
	"regexp"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// MaxBatch caps a single Match request. The mobile spec recommends
// chunking client-side; this is the server's enforcement (handler
// returns 422 above it). Matches WAKEUP.md §6.2 — 1000 hashes.
const MaxBatch = 1000

// hexSHA256 matches a lowercase hex SHA-256 — exactly 64 chars in
// [0-9a-f]. Anchored so a 65-char or mixed-case input fails fast.
var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// UserMatcher is the slice of user repo this package needs.
type UserMatcher interface {
	MatchByEmailHashes(ctx context.Context, hexHashes []string) ([]domain.User, error)
}

// Service is the contacts-match service.
type Service struct {
	users UserMatcher
}

// Config builds the service.
type Config struct {
	Users UserMatcher
}

// New constructs the service. Returns an error if Users is nil.
func New(cfg Config) (*Service, error) {
	if cfg.Users == nil {
		return nil, errors.New("contacts: Config.Users is required")
	}
	return &Service{users: cfg.Users}, nil
}

// Match looks up active users whose email-hash is in `hexHashes`.
//
// Validation:
//   - Empty slice → empty result, no error.
//   - len > MaxBatch → 422 with field "email_hashes".
//   - Any entry not /^[0-9a-f]{64}$/ → 422 with field "email_hashes"
//     and an index hint so the client can fix its chunking.
//
// Privacy:
//   - The service never logs raw hashes (or anything reconstructible
//     from a hash).
//   - Unmatched hashes are not echoed in the response — only matched
//     User rows are returned.
func (s *Service) Match(ctx context.Context, hexHashes []string) ([]domain.User, error) {
	if len(hexHashes) == 0 {
		return nil, nil
	}
	if len(hexHashes) > MaxBatch {
		return nil, apierror.Validation([]apierror.FieldError{{
			Field: "email_hashes", Code: "TOO_MANY",
			Message: "email_hashes exceeds 1000 entries — chunk client-side",
		}})
	}
	for i, h := range hexHashes {
		if !hexSHA256.MatchString(h) {
			return nil, apierror.Validation([]apierror.FieldError{{
				Field: "email_hashes", Code: "INVALID_FORMAT",
				Message: "email_hashes[" + indexString(i) + "] must be a 64-char lowercase hex SHA-256",
			}})
		}
	}
	users, err := s.users.MatchByEmailHashes(ctx, hexHashes)
	if err != nil {
		return nil, apierror.Internal("contacts: match").WithCause(err)
	}
	return users, nil
}

// indexString avoids pulling in fmt for a hot path that may run
// repeatedly inside the validation loop. Index values are ASCII digits.
func indexString(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte // enough for int64 in base 10
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
