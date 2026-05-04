package contacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/contacts"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

func newSvc(t *testing.T) (*contacts.Service, *pgxpool.Pool, *userrepo.Queries) {
	t.Helper()
	pool := testutil.NewTestDB(t)
	users := userrepo.New(pool)
	svc, err := contacts.New(contacts.Config{Users: users})
	if err != nil {
		t.Fatalf("contacts.New: %v", err)
	}
	return svc, pool, users
}

// makeUser inserts a user with a deterministic email so we can hash it
// outside of Postgres and assert the match.
func makeUser(ctx context.Context, t *testing.T, users *userrepo.Queries, email string) domain.User {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	u, err := users.Create(ctx, userrepo.CreateParams{
		ID:           id,
		Username:     "u" + full,
		DisplayName:  "User " + full[:8],
		Email:        email,
		PasswordHash: "h",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func hashEmail(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func TestMatch_FindsByLowercaseHexHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, users := newSvc(t)
	u := makeUser(ctx, t, users, strings.ReplaceAll(uuid.NewString(), "-", "")+"@example.com")

	got, err := svc.Match(ctx, []string{hashEmail(u.Email)})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got) != 1 || got[0].ID != u.ID {
		t.Errorf("got %+v, want one row matching %s", got, u.ID)
	}
}

func TestMatch_UnmatchedHashesNotEchoed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, users := newSvc(t)
	u := makeUser(ctx, t, users, strings.ReplaceAll(uuid.NewString(), "-", "")+"@example.com")
	bogus := strings.Repeat("a", 64)

	got, err := svc.Match(ctx, []string{hashEmail(u.Email), bogus})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got) != 1 || got[0].ID != u.ID {
		t.Errorf("got %d users, want 1 — unmatched hashes shouldn't be echoed", len(got))
	}
}

func TestMatch_SoftDeletedUserExcluded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, users := newSvc(t)
	u := makeUser(ctx, t, users, strings.ReplaceAll(uuid.NewString(), "-", "")+"@example.com")
	if err := users.SoftDelete(ctx, u.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	got, err := svc.Match(ctx, []string{hashEmail(u.Email)})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("soft-deleted user appeared in match: %+v", got)
	}
}

func TestMatch_RejectsMalformedHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newSvc(t)

	cases := []string{
		"",                                       // empty
		strings.Repeat("z", 64),                  // wrong charset
		strings.ToUpper(strings.Repeat("a", 64)), // uppercase not allowed
		strings.Repeat("a", 63),                  // too short
		strings.Repeat("a", 65),                  // too long
	}
	for _, c := range cases {
		_, err := svc.Match(ctx, []string{c})
		var ae *apierror.Error
		if !errors.As(err, &ae) || ae.Code != apierror.CodeValidation {
			t.Errorf("input %q: err = %v, want VALIDATION_FAILED", c, err)
		}
	}
}

func TestMatch_RejectsOversizedBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newSvc(t)

	huge := make([]string, contacts.MaxBatch+1)
	for i := range huge {
		huge[i] = strings.Repeat("a", 64)
	}
	_, err := svc.Match(ctx, huge)
	var ae *apierror.Error
	if !errors.As(err, &ae) || ae.Code != apierror.CodeValidation {
		t.Errorf("err = %v, want VALIDATION_FAILED for over-cap batch", err)
	}
}

func TestMatch_EmptySliceIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _, _ := newSvc(t)
	got, err := svc.Match(ctx, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("empty input: got=%+v err=%v", got, err)
	}
}
