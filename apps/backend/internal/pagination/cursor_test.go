package pagination_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
)

func TestEncode_Decode_RoundTrip(t *testing.T) {
	t.Parallel()
	want := &pagination.Cursor{
		Timestamp: time.Date(2026, 5, 2, 12, 31, 21, 810_000_000, time.UTC),
		ID:        uuid.MustParse("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"),
	}
	encoded := pagination.Encode(want)
	if encoded == "" {
		t.Fatal("Encode returned empty for non-nil cursor")
	}
	got, err := pagination.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", got.Timestamp, want.Timestamp)
	}
	if got.ID != want.ID {
		t.Errorf("ID: got %v, want %v", got.ID, want.ID)
	}
}

func TestEncode_NilReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := pagination.Encode(nil); got != "" {
		t.Fatalf("Encode(nil) = %q, want empty", got)
	}
}

func TestDecode_EmptyReturnsNil(t *testing.T) {
	t.Parallel()
	cases := []string{"", "   ", "\n\t"}
	for _, s := range cases {
		got, err := pagination.Decode(s)
		if err != nil {
			t.Errorf("Decode(%q): unexpected error %v", s, err)
		}
		if got != nil {
			t.Errorf("Decode(%q): expected nil cursor (= first page), got %+v", s, got)
		}
	}
}

func TestDecode_MalformedReturnsBadRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{"not base64", "!!!not-valid-base64!!!"},
		{"base64 of garbage json", "bm90LWpzb24="},                 // "not-json"
		{"empty json object", "e30="},                              // "{}" — both fields zero/nil
		{"missing id", "eyJ0cyI6IjIwMjYtMDUtMDJUMTI6MzE6MjFaIn0="}, // {"ts":"2026-05-02T12:31:21Z"}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := pagination.Decode(tc.raw)
			if err == nil {
				t.Fatalf("expected error for %q, got cursor %+v", tc.raw, c)
			}
			if !pagination.IsInvalidCursor(err) {
				t.Errorf("expected ErrInvalidCursor, got: %v", err)
			}
			// Should be wrapped as a BadRequest apierror.Error.
			var apiErr *apierror.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("Decode error should be *apierror.Error, got %T", err)
			}
			if apiErr.Code != apierror.CodeBadRequest {
				t.Errorf("expected CodeBadRequest, got %q", apiErr.Code)
			}
		})
	}
}

func TestDecode_RejectsCursorForNonexistentRecord(t *testing.T) {
	t.Parallel()
	// Per §6.4: "cursor pointing to a deleted/missing record → returns the
	// next page (no error; the keyset just continues)."
	// The pagination layer can't know whether a record exists — that's the
	// repository's job. Decode just validates STRUCTURE. So a well-formed
	// cursor pointing at an arbitrary UUID + timestamp must succeed; the
	// repo's WHERE (created_at, id) < ($ts, $id) handles the deleted case
	// transparently.
	c := pagination.Cursor{
		Timestamp: time.Now().UTC(),
		ID:        uuid.New(), // certainly not in any DB
	}
	encoded := pagination.Encode(&c)
	got, err := pagination.Decode(encoded)
	if err != nil {
		t.Fatalf("structurally-valid cursor for absent row should decode: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("ID mismatch: got %v, want %v", got.ID, c.ID)
	}
}

func TestPage_OverFetchSignalsHasMore(t *testing.T) {
	t.Parallel()
	type row struct {
		ts time.Time
		id uuid.UUID
	}
	now := time.Now().UTC()
	rows := []row{
		{ts: now.Add(-3 * time.Second), id: uuid.New()},
		{ts: now.Add(-2 * time.Second), id: uuid.New()},
		{ts: now.Add(-1 * time.Second), id: uuid.New()},
		{ts: now, id: uuid.New()}, // the over-fetched extra row
	}

	getCursor := func(r row) pagination.Cursor {
		return pagination.Cursor{Timestamp: r.ts, ID: r.id}
	}

	data, next, hasMore := pagination.Page(rows, 3, getCursor)
	if !hasMore {
		t.Fatal("HasMore should be true when len(rows) > limit")
	}
	if len(data) != 3 {
		t.Fatalf("data trimmed length = %d, want 3", len(data))
	}
	if next == nil {
		t.Fatal("next cursor should not be nil")
	}

	// The next cursor must point at the LAST KEPT row (rows[2]) so the next
	// query starts strictly past it. Decoding and comparing keys proves it.
	got, err := pagination.Decode(*next)
	if err != nil {
		t.Fatalf("Decode next: %v", err)
	}
	if got.ID != rows[2].id {
		t.Fatalf("next cursor ID = %v, want last kept row id %v", got.ID, rows[2].id)
	}
}

func TestPage_NoOverFetchReportsLastPage(t *testing.T) {
	t.Parallel()
	type row struct {
		ts time.Time
		id uuid.UUID
	}
	rows := []row{{ts: time.Now().UTC(), id: uuid.New()}}
	getCursor := func(r row) pagination.Cursor {
		return pagination.Cursor{Timestamp: r.ts, ID: r.id}
	}

	data, next, hasMore := pagination.Page(rows, 5, getCursor)
	if hasMore {
		t.Fatal("HasMore should be false when len(rows) <= limit")
	}
	if next != nil {
		t.Fatalf("next cursor should be nil on last page, got %v", *next)
	}
	if len(data) != 1 {
		t.Fatalf("data length = %d, want 1", len(data))
	}
}

func TestPage_EmptyRowsReportsLastPage(t *testing.T) {
	t.Parallel()
	type row struct{}
	getCursor := func(_ row) pagination.Cursor {
		return pagination.Cursor{Timestamp: time.Now().UTC(), ID: uuid.New()}
	}
	data, next, hasMore := pagination.Page([]row{}, 20, getCursor)
	if hasMore || next != nil || len(data) != 0 {
		t.Fatalf("empty page: got data=%v next=%v hasMore=%v", data, next, hasMore)
	}
}

func TestPage_ZeroLimitFallsBackToDefault(t *testing.T) {
	t.Parallel()
	type row struct {
		id uuid.UUID
	}
	getCursor := func(r row) pagination.Cursor {
		return pagination.Cursor{Timestamp: time.Now().UTC(), ID: r.id}
	}
	// 25 rows with limit=0: Page should treat 0 as DefaultLimit (20) and
	// trim to 20 + report has_more.
	rows := make([]row, 25)
	for i := range rows {
		rows[i].id = uuid.New()
	}
	data, _, hasMore := pagination.Page(rows, 0, getCursor)
	if !hasMore {
		t.Fatal("expected hasMore when row count > DefaultLimit")
	}
	if len(data) != pagination.DefaultLimit {
		t.Fatalf("len = %d, want DefaultLimit %d", len(data), pagination.DefaultLimit)
	}
}

func TestParseLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{"empty defaults", "", pagination.DefaultLimit, false},
		{"zero defaults", "0", pagination.DefaultLimit, false},
		{"valid mid-range", "50", 50, false},
		{"clamps to MaxLimit", "999", pagination.MaxLimit, false},
		{"exact max", "100", 100, false},
		{"negative errors", "-5", 0, true},
		{"non-integer errors", "twenty", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := pagination.ParseLimit(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.raw)
				}
				if !strings.Contains(err.Error(), "limit") {
					t.Errorf("error should mention 'limit': %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseLimit(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}
