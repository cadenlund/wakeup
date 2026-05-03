package fixtures_test

import (
	"context"
	"fmt"
	"strings"
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
// fixture for X live?" rather than as functional code. We assert
// both that the panic fires AND that the recovered value mentions
// the milestone pointer, so a future stub that swaps in a generic
// "not implemented" message or a bare nil-deref would surface here.
func TestStubs_PanicWithMilestonePointer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		fn      func(t *testing.T)
		wantSub string
	}{
		{
			name: "MakeFriendship",
			fn: func(t *testing.T) {
				var u domain.User
				fixtures.MakeFriendship(t, nil, u, u)
			},
			wantSub: "milestone 4.1",
		},
		{
			name: "MakeConversation",
			fn: func(t *testing.T) {
				fixtures.MakeConversation(t, nil, nil)
			},
			wantSub: "milestone 5.1",
		},
		{
			name: "MakeMessage",
			fn: func(t *testing.T) {
				var u domain.User
				fixtures.MakeMessage(t, nil, nil, u)
			},
			wantSub: "milestone 6.1",
		},
		{
			name: "MakeAttachment",
			fn: func(t *testing.T) {
				var u domain.User
				fixtures.MakeAttachment(t, nil, u)
			},
			wantSub: "milestone 7.1",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				r := recover()
				if r == nil {
					t.Errorf("%s: expected panic", tc.name)
					return
				}
				msg := fmt.Sprint(r)
				if !strings.Contains(msg, tc.wantSub) {
					t.Errorf("%s: panic %q missing milestone pointer %q", tc.name, msg, tc.wantSub)
				}
			}()
			tc.fn(t)
		})
	}
}
