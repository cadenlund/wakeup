// Package fixtures builds aggregate entities for tests. Each MakeX function
// inserts a deterministic-randomized record and returns the populated
// domain type so test code can reference IDs / fields without hand-rolling
// SQL or wiring through service calls.
//
// Per WAKEUP.md §12.6: fixture defaults are randomized enough to avoid
// collisions but reproducible enough to debug. Usernames, emails, etc.
// embed a short UUID v7 prefix so log lines stay searchable.
package fixtures

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// userBuilder accumulates options. MakeUser materializes one of these and
// runs the INSERT. All optional fields default to deterministic-random values
// derived from a fresh UUID v7 so collisions across parallel tests are
// avoided without sacrificing reproducibility.
type userBuilder struct {
	id           uuid.UUID
	username     string
	displayName  string
	email        string
	passwordHash string
	role         string
	colorScheme  string
	softDeleted  bool
}

// UserOpt customizes MakeUser. All options are functional.
type UserOpt func(*userBuilder)

// WithUsername sets the username. Default is `fixture-<short-uuid>`.
func WithUsername(s string) UserOpt { return func(b *userBuilder) { b.username = s } }

// WithEmail sets the email. Default is `<username>@fixtures.test`.
func WithEmail(s string) UserOpt { return func(b *userBuilder) { b.email = s } }

// WithDisplayName sets the display name. Default is `Test User <short-uuid>`.
func WithDisplayName(s string) UserOpt { return func(b *userBuilder) { b.displayName = s } }

// WithPasswordHash sets the stored password hash directly. Default is the
// argon2id hash of `Password123!` (Phase 3 will pre-compute one and inject;
// for now the literal string `fixture-hash` is a stand-in since auth tests
// land in Phase 3.6 anyway).
func WithPasswordHash(s string) UserOpt { return func(b *userBuilder) { b.passwordHash = s } }

// WithRole sets `user` (default) or `admin`. Other values fail the schema
// CHECK constraint and return a DB error from MakeUser.
func WithRole(role string) UserOpt { return func(b *userBuilder) { b.role = role } }

// WithColorScheme sets `light` / `dark` / `system` (default `system`).
func WithColorScheme(s string) UserOpt { return func(b *userBuilder) { b.colorScheme = s } }

// WithSoftDeleted marks the user as soft-deleted (deleted_at = now()).
// Useful for §4.6 soft-delete invariant tests.
func WithSoftDeleted() UserOpt { return func(b *userBuilder) { b.softDeleted = true } }

// MakeUser inserts a user with sensible defaults and returns the populated
// domain.User. t.Cleanup is NOT registered — the test's pgtestdb is dropped
// at end of test, taking the row with it.
func MakeUser(t *testing.T, db *pgxpool.Pool, opts ...UserOpt) domain.User {
	t.Helper()
	b := &userBuilder{
		id:           uuid.Must(uuid.NewV7()),
		role:         "user",
		colorScheme:  "system",
		passwordHash: "fixture-hash", // real argon2id hashes land in Phase 3.6
	}
	for _, opt := range opts {
		opt(b)
	}

	short := strings.ReplaceAll(b.id.String()[:8], "-", "")
	if b.username == "" {
		b.username = "fixture-" + short
	}
	if b.email == "" {
		b.email = b.username + "@fixtures.test"
	}
	if b.displayName == "" {
		b.displayName = "Test User " + short
	}

	deletedAtClause := ""
	args := []any{
		b.id, b.username, b.displayName, b.email,
		b.passwordHash, b.role, b.colorScheme,
	}
	if b.softDeleted {
		deletedAtClause = ", deleted_at"
		args = append(args, time.Now().UTC())
	}

	query := `
		INSERT INTO users (id, username, display_name, email, password_hash, role, color_scheme` +
		deletedAtClause + `)
		VALUES ($1, $2, $3, $4, $5, $6, $7` + softDeleteParam(b.softDeleted) + `)
		RETURNING id, username, display_name, email, password_hash, avatar_url,
		          color_scheme, role, created_at, updated_at, deleted_at
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var u domain.User
	err := db.QueryRow(ctx, query, args...).Scan(
		&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.PasswordHash,
		&u.AvatarURL, &u.ColorScheme, &u.Role, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if err != nil {
		t.Fatalf("fixtures.MakeUser: %v", err)
	}
	return u
}

func softDeleteParam(soft bool) string {
	if soft {
		return ", $8"
	}
	return ""
}
