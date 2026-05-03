package idempotency

import (
	"time"

	"github.com/google/uuid"
)

// Entry mirrors a row in idempotency_keys. The middleware (§4.8) compares
// RequestHash to detect duplicate-key-with-different-body and replays
// (ResponseStatus, ResponseHeaders, ResponseBody) on a cache hit.
//
// There is no domain.IdempotencyKey type because nothing outside the
// idempotency middleware reasons about these rows — keeping it repo-local
// avoids polluting the shared domain package.
type Entry struct {
	Key             string
	UserID          uuid.UUID
	RequestHash     []byte // 32 bytes (SHA-256), enforced at the DB level
	ResponseStatus  int
	ResponseHeaders map[string][]string // nil for rows written before migration 0013
	ResponseBody    []byte
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

// PutParams is the input to Queries.Put. The TTL is converted to an absolute
// expires_at inside the repo so the schema receives a timestamptz instead of
// an interval string.
type PutParams struct {
	Key             string
	UserID          uuid.UUID
	RequestHash     []byte
	ResponseStatus  int
	ResponseHeaders map[string][]string
	ResponseBody    []byte
	TTL             time.Duration
}
