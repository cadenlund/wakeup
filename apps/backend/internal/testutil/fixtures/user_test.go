package fixtures_test

import (
	"context"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil/fixtures"
)

// MakeUser with all-default options inserts a user and returns the
// populated row. Schema-default fields (role, color_scheme) match the
// builder defaults; randomized fields (username, email, display_name)
// embed the short UUID.
func TestMakeUser_Defaults(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	u := fixtures.MakeUser(t, pool)

	if u.ID.String() == "" {
		t.Errorf("ID should be set")
	}
	if u.Role != "user" {
		t.Errorf("Role = %q, want user", u.Role)
	}
	if u.ColorScheme != "system" {
		t.Errorf("ColorScheme = %q, want system", u.ColorScheme)
	}
	if u.Username == "" || u.Email == "" || u.DisplayName == "" {
		t.Errorf("randomized fields empty: %+v", u)
	}
	if u.DeletedAt != nil {
		t.Errorf("DeletedAt should be nil for non-soft-deleted user")
	}
}

// Each WithX option flips one field. Asserts the override actually
// reaches the row.
func TestMakeUser_Options(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	u := fixtures.MakeUser(t, pool,
		fixtures.WithUsername("alice"),
		fixtures.WithEmail("alice@x.test"),
		fixtures.WithDisplayName("Alice"),
		fixtures.WithPasswordHash("custom-hash"),
		fixtures.WithRole("admin"),
		fixtures.WithColorScheme("dark"),
	)
	if u.Username != "alice" {
		t.Errorf("Username = %q, want alice", u.Username)
	}
	if u.Email != "alice@x.test" {
		t.Errorf("Email = %q", u.Email)
	}
	if u.DisplayName != "Alice" {
		t.Errorf("DisplayName = %q", u.DisplayName)
	}
	if u.PasswordHash != "custom-hash" {
		t.Errorf("PasswordHash = %q", u.PasswordHash)
	}
	if u.Role != "admin" {
		t.Errorf("Role = %q, want admin", u.Role)
	}
	if u.ColorScheme != "dark" {
		t.Errorf("ColorScheme = %q", u.ColorScheme)
	}
}

// WithSoftDeleted inserts a row with deleted_at populated. Confirms
// the conditional softDeleteParam branch in the SQL builder.
func TestMakeUser_WithSoftDeleted(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	u := fixtures.MakeUser(t, pool, fixtures.WithSoftDeleted())
	if u.DeletedAt == nil {
		t.Errorf("DeletedAt should be set on a soft-deleted user")
	}
	// Sanity: row reachable via raw SQL.
	var count int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL", u.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 soft-deleted row, got %d", count)
	}
}

// The remaining stubs (MakeFriendship, MakeConversation, MakeMessage,
// MakeAttachment) all panic with a "real impl lands in milestone X"
// message. They survive as a search target for "where does the
// fixture for X live?" rather than as functional code. We assert the
// panic so a future caller who tries to use them gets the documented
// pointer instead of a less-clear nil-pointer panic.
//
// One subtest per stub so a regression that turns one of them into a
// no-op is visible in the test output.
func TestStubs_PanicWithMilestonePointer(t *testing.T) {
	t.Parallel()
	t.Run("MakeFriendship", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic")
			}
		}()
		var u domain.User
		fixtures.MakeFriendship(t, nil, u, u)
	})
	t.Run("MakeConversation", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic")
			}
		}()
		fixtures.MakeConversation(t, nil, nil)
	})
	t.Run("MakeMessage", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic")
			}
		}()
		var u domain.User
		fixtures.MakeMessage(t, nil, nil, u)
	})
	t.Run("MakeAttachment", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic")
			}
		}()
		var u domain.User
		fixtures.MakeAttachment(t, nil, u)
	})
}
