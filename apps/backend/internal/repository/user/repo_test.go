package user_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeBaseParams builds a CreateParams with a unique username + email
// derived from a fresh UUID v7 so parallel tests don't collide on the
// users table's UNIQUE constraints.
//
// The first 8 chars of a UUID v7 are pure timestamp and can collide within
// the same millisecond; we drop the hyphens and use the full 32-char hex
// for uniqueness even at high parallelism.
func makeBaseParams() user.CreateParams {
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	return user.CreateParams{
		ID:           id,
		Username:     "u" + full,
		DisplayName:  "User " + full[:8],
		Email:        full + "@test",
		PasswordHash: "hash",
	}
}

// helper to build a repo + clean pool for each test.
func newRepo(t *testing.T) (*user.Queries, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.NewTestDB(t)
	return user.New(pool), pool
}

func TestCreate_PersistsAllFields(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	p := makeBaseParams()
	got, err := repo.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID != p.ID || got.Username != p.Username || got.Email != p.Email || got.DisplayName != p.DisplayName {
		t.Fatalf("identity mismatch: got %+v want %+v", got, p)
	}
	if got.Role != "user" || got.ColorScheme != "system" {
		t.Errorf("defaults wrong: role=%q color=%q", got.Role, got.ColorScheme)
	}
	if got.DeletedAt != nil {
		t.Errorf("DeletedAt should be nil on fresh user")
	}
}

func TestGetByID_ExcludesSoftDeleted(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	created, err := repo.Create(ctx, makeBaseParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch")
	}

	// Soft-delete; GetByID must now return ErrNotFound.
	if err := repo.SoftDelete(ctx, created.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("after SoftDelete: expected ErrNotFound, got %v", err)
	}
}

func TestGetByIDIncludingDeleted_FindsSoftDeleted(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	created, err := repo.Create(ctx, makeBaseParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SoftDelete(ctx, created.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	got, err := repo.GetByIDIncludingDeleted(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByIDIncludingDeleted: %v", err)
	}
	if got.DeletedAt == nil {
		t.Fatal("expected DeletedAt to be set on returned user")
	}
}

func TestGetByID_MissReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	if _, err := repo.GetByID(context.Background(), uuid.New()); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetByUsername_AndEmail(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	p := makeBaseParams()
	created, err := repo.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Username lookup uses citext so case shouldn't matter — but just check
	// canonical case here; case-insensitivity is a citext-property test we
	// don't need to repeat.
	gotU, err := repo.GetByUsername(ctx, created.Username)
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if gotU.ID != created.ID {
		t.Errorf("GetByUsername ID mismatch")
	}

	gotE, err := repo.GetByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if gotE.ID != created.ID {
		t.Errorf("GetByEmail ID mismatch")
	}

	// Misses return ErrNotFound.
	if _, err := repo.GetByUsername(ctx, "no-such-user"); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("GetByUsername miss: %v", err)
	}
	if _, err := repo.GetByEmail(ctx, "no-such@example.test"); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("GetByEmail miss: %v", err)
	}
}

func TestUpdate_PatchesOnlyProvidedFields(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	created, err := repo.Create(ctx, makeBaseParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newName := "New Display"
	newScheme := "dark"
	got, err := repo.Update(ctx, user.UpdateParams{
		ID:          created.ID,
		DisplayName: &newName,
		ColorScheme: &newScheme,
		// AvatarURL nil → unchanged
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.DisplayName != newName || got.ColorScheme != newScheme {
		t.Errorf("Update didn't apply: %+v", got)
	}
	if got.Username != created.Username || got.Email != created.Email {
		t.Errorf("non-patched fields should be unchanged: %+v", got)
	}
}

func TestUpdate_OfMissingUserReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	name := "x"
	_, err := repo.Update(context.Background(), user.UpdateParams{
		ID:          uuid.New(),
		DisplayName: &name,
	})
	if !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSoftDelete_OnMissingReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	if err := repo.SoftDelete(context.Background(), uuid.New()); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListByPrefix_Empty(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	got, err := repo.ListByPrefix(context.Background(), "", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty DB, q=\"\" should return 0 rows, got %d", len(got))
	}
}

func TestListByPrefix_QEmptyReturnsAll(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := repo.Create(ctx, makeBaseParams())
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	got, err := repo.ListByPrefix(ctx, "", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5, got %d", len(got))
	}
}

func TestListByPrefix_PrefixMatch(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	// One user whose username starts with "caden", four others not.
	caden := makeBaseParams()
	caden.Username = "caden_special"
	caden.DisplayName = "Caden Lund"
	caden.Email = caden.Username + "@x.test"
	if _, err := repo.Create(ctx, caden); err != nil {
		t.Fatalf("Create caden: %v", err)
	}
	for i := 0; i < 4; i++ {
		p := makeBaseParams()
		// ListByPrefix is substring search (`ILIKE '%cad%'`), so a bare
		// "other-" prefix isn't enough — the UUID-derived hex tail can
		// incidentally contain "cad" (CI hit this when a generated id
		// was `019e14f6...2cad7984...`). Scrub any "cad" out of the hex
		// before assembling so the non-match invariant holds regardless
		// of which UUID we draw.
		p.Username = "other-" + strings.ReplaceAll(p.Username, "cad", "zzz")
		p.DisplayName = "Other"
		if _, err := repo.Create(ctx, p); err != nil {
			t.Fatalf("Create other %d: %v", i, err)
		}
	}

	got, err := repo.ListByPrefix(ctx, "cad", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d (rows: %+v)", len(got), got)
	}
	if got[0].User.Username != "caden_special" {
		t.Errorf("matched row wrong: %+v", got[0])
	}
}

// TestListByPrefix_ExactMatchRanksFirst regresses the match-rank
// ordering: an exact username match leads, then username-prefix
// matches, regardless of which was created more recently. (Before,
// the only tiebreaker within a tier was created_at DESC, so a newer
// "user499" outranked the exact "user4".)
func TestListByPrefix_ExactMatchRanksFirst(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	// Create the EXACT match FIRST (so it's the oldest) and the
	// prefix matches after — under the old recency-only ordering
	// (created_at DESC within a tier) the exact match would have
	// landed last; match-rank ordering must float it to the top.
	suffix := makeBaseParams().Username[1:9] // 8 hex chars — unique enough
	mk := func(name string) {
		p := makeBaseParams()
		p.Username = name
		p.DisplayName = name
		p.Email = name + "-" + suffix + "@x.test"
		if _, err := repo.Create(ctx, p); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	base := "qz" + suffix // unlikely to collide with other test rows
	mk(base)              // exact match, created first (oldest)
	mk(base + "99")       // username-prefix match
	mk(base + "7")        // username-prefix match, created last (newest)

	got, err := repo.ListByPrefix(ctx, base, nil, nil, 10)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) < 3 {
		t.Fatalf("expected ≥3 hits, got %d (%+v)", len(got), got)
	}
	if got[0].User.Username != base {
		t.Fatalf("exact match should rank first; got %q (rows: %+v)", got[0].User.Username, got)
	}
	if got[0].MatchRank != 0 {
		t.Errorf("exact match should have MatchRank 0, got %d", got[0].MatchRank)
	}
	for _, h := range got[1:3] {
		if h.MatchRank != 1 {
			t.Errorf("prefix match %q should have MatchRank 1, got %d", h.User.Username, h.MatchRank)
		}
	}
}

// LIKE-metacharacter input must NOT behave as a wildcard. A user typing
// "%" into the search box should see only literal-percent matches, not
// every row.
func TestListByPrefix_EscapesWildcardChars(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	// Two users: one whose username starts with literal "%", another with
	// a totally different prefix. A naive ILIKE without escape would
	// treat "%" as wildcard and return BOTH.
	literalPercent := makeBaseParams()
	literalPercent.Username = "%percent_user"
	literalPercent.DisplayName = "%pct"
	literalPercent.Email = "pct@x.test"
	if _, err := repo.Create(ctx, literalPercent); err != nil {
		t.Fatalf("Create literal: %v", err)
	}
	other := makeBaseParams()
	if _, err := repo.Create(ctx, other); err != nil {
		t.Fatalf("Create other: %v", err)
	}

	got, err := repo.ListByPrefix(ctx, "%", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	// Without the escape, both would match. With escape, only the literal
	// one (whose username actually starts with "%") matches.
	if len(got) != 1 {
		t.Fatalf("expected 1 row (only the literal-%% user), got %d: %+v", len(got), got)
	}
	if got[0].User.Username != "%percent_user" {
		t.Errorf("matched the wrong row: %+v", got[0])
	}
}

func TestListByPrefix_ExcludesSoftDeleted(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	p := makeBaseParams()
	p.Username = "deleted-bob"
	p.DisplayName = "Bob"
	p.Email = "bob@x.test"
	created, err := repo.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SoftDelete(ctx, created.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	got, err := repo.ListByPrefix(ctx, "deleted", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("soft-deleted users must not appear in search, got %d", len(got))
	}
}

// Pagination edge: with N+1 rows and limit N, we over-fetch N+1; the
// service-layer's pagination.Page trims to N and reports has_more.
func TestListByPrefix_PaginationOverFetch(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		_, err := repo.Create(ctx, makeBaseParams())
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	// Limit 2 → over-fetch 3 → returns 3 rows.
	got, err := repo.ListByPrefix(ctx, "", nil, nil, 2)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 (limit+1) rows for over-fetch, got %d", len(got))
	}
}

func TestListByPrefix_CursorAdvances(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	created := make([]uuid.UUID, 0, 4)
	for i := 0; i < 4; i++ {
		u, err := repo.Create(ctx, makeBaseParams())
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		created = append(created, u.ID)
	}

	page1, err := repo.ListByPrefix(ctx, "", nil, nil, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	// Use the LAST KEPT row (index limit-1 = 1) as the next cursor.
	// The cursor must include the tier so the new keyset chain
	// resumes inside the same rel_tier bucket; here the rows have
	// no caller context so they all land in tier 2.
	tier := page1[1].Tier
	cursor := &pagination.Cursor{
		Timestamp: page1[1].User.CreatedAt,
		ID:        page1[1].User.ID,
		Tier:      &tier,
	}
	page2, err := repo.ListByPrefix(ctx, "", nil, cursor, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	// page2 must not contain rows from page1.
	for _, p2 := range page2 {
		for _, p1 := range page1[:2] {
			if p1.User.ID == p2.User.ID {
				t.Errorf("page2 contains row from page1: %v", p1.User.ID)
			}
		}
	}
	_ = created
}

func TestListByIDs_ReturnsRequestedIncludingSoftDeleted(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	a, err := repo.Create(ctx, makeBaseParams())
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := repo.Create(ctx, makeBaseParams())
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	if err := repo.SoftDelete(ctx, b.ID); err != nil {
		t.Fatalf("SoftDelete b: %v", err)
	}

	got, err := repo.ListByIDs(ctx, []uuid.UUID{a.ID, b.ID, uuid.New()})
	if err != nil {
		t.Fatalf("ListByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows (deleted included, missing skipped), got %d", len(got))
	}
}

func TestListByIDs_EmptyInputReturnsNil(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	got, err := repo.ListByIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListByIDs(nil): %v", err)
	}
	if got != nil {
		t.Fatalf("ListByIDs(nil) should be nil, got %v", got)
	}
}

func TestUpdatePassword_RoundTrip(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	created, err := repo.Create(ctx, makeBaseParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdatePassword(ctx, created.ID, "new-hash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PasswordHash != "new-hash" {
		t.Fatalf("password not updated, got %q", got.PasswordHash)
	}
}

func TestUpdateRole_RoundTrip(t *testing.T) {
	t.Parallel()
	repo, _ := newRepo(t)
	ctx := context.Background()

	created, err := repo.Create(ctx, makeBaseParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdateRole(ctx, created.ID, "admin"); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Role != "admin" {
		t.Fatalf("role not updated, got %q", got.Role)
	}
}
