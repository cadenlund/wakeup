package audit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/audit"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a user via raw SQL so the FK from audit_log is
// satisfied. Same trick used in other repo tests.
func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u"+full, full+"@x.test")
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }
func ptrStr(s string) *string         { return &s }

// --- Create --------------------------------------------------------------

func TestCreate_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)
	target := makeUser(ctx, t, pool)

	id := uuid.Must(uuid.NewV7())
	if err := repo.Create(ctx, audit.CreateParams{
		ID:         id,
		ActorID:    &actor,
		Action:     "user.update",
		TargetType: ptrStr("user"),
		TargetID:   ptrUUID(target),
		Metadata:   map[string]any{"role": "admin"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := repo.List(ctx, audit.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.ID != id {
		t.Errorf("ID mismatch")
	}
	if got.ActorID == nil || *got.ActorID != actor {
		t.Errorf("ActorID mismatch: %v", got.ActorID)
	}
	if got.Action != "user.update" {
		t.Errorf("Action = %q", got.Action)
	}
	if got.Metadata["role"] != "admin" {
		t.Errorf("Metadata.role = %v", got.Metadata["role"])
	}
}

func TestCreate_BookendStartedHasNilTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)
	target := makeUser(ctx, t, pool)

	// impersonate.started bookend per §8.7: actor=admin, action="impersonate.started",
	// metadata.impersonating_user_id=target. Target as foreign id, not target_id.
	id := uuid.Must(uuid.NewV7())
	if err := repo.Create(ctx, audit.CreateParams{
		ID: id, ActorID: &actor, Action: "impersonate.started",
		Metadata: map[string]any{"impersonating_user_id": target.String()},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := repo.List(ctx, audit.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if rows[0].TargetType != nil {
		t.Errorf("TargetType should be nil, got %v", rows[0].TargetType)
	}
	if rows[0].TargetID != nil {
		t.Errorf("TargetID should be nil, got %v", rows[0].TargetID)
	}
	if rows[0].Metadata["impersonating_user_id"] != target.String() {
		t.Errorf("metadata.impersonating_user_id = %v", rows[0].Metadata["impersonating_user_id"])
	}
}

func TestCreate_NilMetadataWritesSQLNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)

	if err := repo.Create(ctx, audit.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ActorID: &actor, Action: "system.startup",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Direct SQL: metadata IS NULL — not the literal jsonb 'null'.
	var nullCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM audit_log WHERE metadata IS NULL").Scan(&nullCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if nullCount != 1 {
		t.Errorf("expected 1 NULL-metadata row, got %d", nullCount)
	}
}

func TestCreate_NilActorIsAllowed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)

	// system.* actions have no acting user — schema's actor_id has no
	// NOT NULL constraint, so this is valid.
	if err := repo.Create(ctx, audit.CreateParams{
		ID: uuid.Must(uuid.NewV7()), Action: "system.startup",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestCreate_RejectsEmptyAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)

	if err := repo.Create(ctx, audit.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ActorID: &actor, Action: "",
	}); err == nil {
		t.Error("expected error for empty Action")
	}
}

func TestCreate_RejectsNilID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)

	if err := repo.Create(ctx, audit.CreateParams{
		ActorID: &actor, Action: "user.update",
	}); err == nil {
		t.Error("expected error for zero ID")
	}
}

// --- List ----------------------------------------------------------------

func TestList_NewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)

	for i := 0; i < 3; i++ {
		if err := repo.Create(ctx, audit.CreateParams{
			ID: uuid.Must(uuid.NewV7()), ActorID: &actor, Action: "user.update",
			Metadata: map[string]any{"i": float64(i)},
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		// Tiny gap to ensure distinct created_at values.
		time.Sleep(2 * time.Millisecond)
	}

	rows, err := repo.List(ctx, audit.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// Newest first: i=2, i=1, i=0.
	for idx, want := range []float64{2, 1, 0} {
		if rows[idx].Metadata["i"] != want {
			t.Errorf("rows[%d].i = %v, want %v", idx, rows[idx].Metadata["i"], want)
		}
	}
}

func TestList_PaginatesWithCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)

	for i := 0; i < 5; i++ {
		if err := repo.Create(ctx, audit.CreateParams{
			ID: uuid.Must(uuid.NewV7()), ActorID: &actor, Action: "user.update",
			Metadata: map[string]any{"i": float64(i)},
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	first, err := repo.List(ctx, audit.ListParams{Limit: 2})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(first) != 3 {
		// limit=2 → over-fetch=3.
		t.Fatalf("expected over-fetched 3 rows on page 1, got %d", len(first))
	}
	// Cursor is the LAST row of the truncated page (index limit-1, i.e. 1).
	// pagination.Page would chop the slice to limit and emit this row's
	// (created_at, id) as next_cursor.
	cursor := &pagination.Cursor{
		Timestamp: first[1].CreatedAt,
		ID:        first[1].ID,
	}
	page2, err := repo.List(ctx, audit.ListParams{Cursor: cursor, Limit: 10})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	// page1 returned i=4, i=3, i=2 (over-fetch). page2 (after cursor at i=3)
	// should yield i=2, i=1, i=0 (3 rows).
	if len(page2) != 3 {
		t.Fatalf("expected 3 rows on page 2, got %d", len(page2))
	}
	if page2[0].Metadata["i"].(float64) >= first[1].Metadata["i"].(float64) {
		t.Errorf("page 2 should start strictly before cursor row: page2[0].i=%v cursor.i=%v",
			page2[0].Metadata["i"], first[1].Metadata["i"])
	}
}

func TestList_ClampsLimitToMax(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)

	// Seed a few rows so the LIMIT clamp affects an observable result
	// (not strictly necessary — the clamp is a code-path check).
	for i := 0; i < 3; i++ {
		if err := repo.Create(ctx, audit.CreateParams{
			ID: uuid.Must(uuid.NewV7()), ActorID: &actor, Action: "user.update",
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	// Limit far above MaxLimit must not blow up; just returns at most MaxLimit+1 rows.
	rows, err := repo.List(ctx, audit.ListParams{Limit: 10_000_000})
	if err != nil {
		t.Fatalf("List with huge limit: %v", err)
	}
	if len(rows) > pagination.MaxLimit+1 {
		t.Errorf("expected at most MaxLimit+1 rows, got %d", len(rows))
	}
}

func TestList_DefaultLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)
	actor := makeUser(ctx, t, pool)

	if err := repo.Create(ctx, audit.CreateParams{
		ID: uuid.Must(uuid.NewV7()), ActorID: &actor, Action: "user.update",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Limit=0 should fall through to DefaultLimit (20).
	rows, err := repo.List(ctx, audit.ListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
}

func TestList_EmptyTableReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := audit.New(pool)

	rows, err := repo.List(ctx, audit.ListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if rows == nil {
		t.Errorf("expected non-nil empty slice for ranging")
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}
