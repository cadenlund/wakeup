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
	got, err := repo.ListByPrefix(context.Background(), "", nil, 10)
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

	got, err := repo.ListByPrefix(ctx, "", nil, 10)
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
		p.Username = "other-" + p.Username // ensure no "caden" prefix
		p.DisplayName = "Other"
		if _, err := repo.Create(ctx, p); err != nil {
			t.Fatalf("Create other %d: %v", i, err)
		}
	}

	got, err := repo.ListByPrefix(ctx, "cad", nil, 10)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d (rows: %+v)", len(got), got)
	}
	if got[0].Username != "caden_special" {
		t.Errorf("matched row wrong: %+v", got[0])
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

	got, err := repo.ListByPrefix(ctx, "%", nil, 10)
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	// Without the escape, both would match. With escape, only the literal
	// one (whose username actually starts with "%") matches.
	if len(got) != 1 {
		t.Fatalf("expected 1 row (only the literal-%% user), got %d: %+v", len(got), got)
	}
	if got[0].Username != "%percent_user" {
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

	got, err := repo.ListByPrefix(ctx, "deleted", nil, 10)
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
	got, err := repo.ListByPrefix(ctx, "", nil, 2)
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

	page1, err := repo.ListByPrefix(ctx, "", nil, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	// Use the LAST KEPT row (index limit-1 = 1) as the next cursor.
	cursor := &pagination.Cursor{
		Timestamp: page1[1].CreatedAt,
		ID:        page1[1].ID,
	}
	page2, err := repo.ListByPrefix(ctx, "", cursor, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	// page2 must not contain rows from page1.
	for _, p2 := range page2 {
		for _, p1 := range page1[:2] {
			if p1.ID == p2.ID {
				t.Errorf("page2 contains row from page1: %v", p1.ID)
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
