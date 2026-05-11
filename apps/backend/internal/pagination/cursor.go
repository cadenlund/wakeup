// Package pagination is the keyset-cursor toolkit every list endpoint uses.
// Per WAKEUP.md §6.4: the API never uses OFFSET — every list page is keyed
// on (created_at, id) so concurrent inserts can't shift rows between pages.
//
// Three pieces:
//
//   - Cursor + Encode / Decode — base64-JSON envelope passed in `cursor`
//     query params (clients treat it as opaque).
//   - Page[T] — repository List* methods over-fetch limit+1 rows; this helper
//     trims to the page, computes next_cursor from the last row, and reports
//     has_more.
//   - ParseLimit — clamps `limit` query param to the §6.1 defaults
//     (default 20, max 100).
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
)

// DefaultLimit and MaxLimit are the §6.1 defaults applied when a list endpoint
// receives an empty or out-of-range `limit` query param.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Cursor is the opaque value sent to/from clients as a base64 string. The
// (Timestamp, ID) pair tie-breaks rows that share a microsecond — without ID
// the keyset can drop or duplicate rows when timestamps collide.
//
// Tier and MatchRank are optional secondary keyset slots for endpoints
// whose ORDER BY starts with rank columns ahead of (timestamp, id). For
// /v1/users search the sort is (rel_tier ASC, match_rank ASC, created_at
// DESC, id DESC): Tier ranks friends → pending → strangers; MatchRank
// ranks exact-username → username-prefix → display-name-* → substring so
// the closest match leads the page. When non-nil each sits LEFT of the
// (timestamp, id) pair and LEFT of any field after it. Endpoints that
// don't use a slot leave it nil and the field omitempty-drops from the
// encoded cursor, so existing clients see the same shape.
type Cursor struct {
	Timestamp time.Time `json:"ts"`
	ID        uuid.UUID `json:"id"`
	Tier      *int      `json:"tier,omitempty"`
	MatchRank *int      `json:"mr,omitempty"`
}

// ErrInvalidCursor is the typed error Decode returns on malformed input.
// Wrapped in apierror.BadRequest so handlers can translate to 400 + the
// stable wire code "INVALID_CURSOR" without rewrapping in every handler.
var ErrInvalidCursor = apierror.BadRequest("invalid cursor")

// Encode returns the base64-URL envelope of c. Returns an empty string for a
// nil cursor (the "first page" sentinel).
func Encode(c *Cursor) string {
	if c == nil {
		return ""
	}
	raw, err := json.Marshal(c)
	if err != nil {
		// json.Marshal of a struct with public fields can't fail under
		// any input we'd hand it — but if it ever does, a panic is the
		// only honest answer; we don't want to silently emit "" and
		// confuse a client into thinking they're at the last page.
		panic(fmt.Errorf("pagination: cursor marshal: %w", err))
	}
	return base64.URLEncoding.EncodeToString(raw)
}

// Decode parses a base64-URL envelope. Returns:
//
//   - (nil, nil) for empty input — clients pass no cursor on the first page.
//   - (cursor, nil) on success.
//   - (nil, ErrInvalidCursor) on malformed input — already shaped as
//     apierror.BadRequest, so handlers can return it verbatim via WriteError.
//
// Trims surrounding whitespace so a copy-pasted cursor with stray newlines
// doesn't 400 spuriously.
func Decode(s string) (*Cursor, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, ErrInvalidCursor
	}
	if c.Timestamp.IsZero() || c.ID == uuid.Nil {
		return nil, ErrInvalidCursor
	}
	return &c, nil
}

// Page consumes the over-fetched slice (the repository asked for limit+1 rows)
// and returns the trimmed data, the next-page cursor as an opaque base64
// string, and whether there are more pages. getCursor extracts the keyset
// fields from a row.
//
//   - rows has len < limit+1 → all results fit; data=rows, next=nil, hasMore=false.
//   - rows has len == limit+1 → trim to limit, compute next from the last
//     KEPT row (so the next query starts strictly past it).
//   - limit <= 0 → treated as DefaultLimit so a misuse can't return everything.
func Page[T any](rows []T, limit int, getCursor func(T) Cursor) (data []T, next *string, hasMore bool) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if len(rows) > limit {
		kept := rows[:limit]
		c := getCursor(kept[len(kept)-1])
		encoded := Encode(&c)
		return kept, &encoded, true
	}
	return rows, nil, false
}

// ParseLimit interprets the `limit` query param. Empty/missing/zero falls to
// DefaultLimit. Negative is an error. Values above MaxLimit are clamped to
// MaxLimit (NOT an error — clients sending limit=200 just get 100, the same
// way SQL clamps don't 400).
func ParseLimit(raw string) (int, error) {
	if raw == "" {
		return DefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apierror.BadRequest("invalid limit: must be an integer")
	}
	if n < 0 {
		return 0, apierror.BadRequest("invalid limit: must be >= 0")
	}
	if n == 0 {
		return DefaultLimit, nil
	}
	if n > MaxLimit {
		return MaxLimit, nil
	}
	return n, nil
}

// IsInvalidCursor reports whether err is (or wraps) the malformed-cursor
// sentinel. Useful in service-layer handler-side branching.
func IsInvalidCursor(err error) bool {
	return errors.Is(err, ErrInvalidCursor)
}
