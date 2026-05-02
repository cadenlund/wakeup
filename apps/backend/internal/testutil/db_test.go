package testutil_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// NewTestDB itself is what other tests use for DB isolation. These tests
// validate that:
//   1. it returns a working pool against a per-test cloned DB
//   2. migrations have been applied (the users table exists with the schema
//      from §5.1)
//   3. parallel callers get isolated DBs (a row inserted in one test is
//      invisible in another)

func TestNewTestDB_AppliesMigrations(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// users table must exist (migration 0001_init) with the schema we expect.
	var count int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected empty users table, got %d rows", count)
	}

	// CHECK constraint on users.color_scheme should reject a bad value.
	id := uuid.NewString()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash, color_scheme)
		VALUES ($1, 'a', 'A', 'a@example.com', 'x', 'bogus')
	`, id)
	if err == nil {
		t.Fatal("expected color_scheme CHECK violation, got nil error")
	}
}

func TestNewTestDB_ParallelIsolation(t *testing.T) {
	t.Parallel()
	// alphaInserted is closed AFTER alpha commits its row, so beta only
	// queries against a state where the row would be visible if isolation
	// were broken. Without this barrier the test could pass spuriously
	// when beta runs first.
	alphaInserted := make(chan struct{})

	t.Run("alpha", func(t *testing.T) {
		t.Parallel()
		defer close(alphaInserted)
		pool := testutil.NewTestDB(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(ctx, `
			INSERT INTO users (id, username, display_name, email, password_hash)
			VALUES ($1, 'alpha', 'Alpha', 'alpha@example.com', 'x')
		`, uuid.NewString())
		if err != nil {
			t.Fatalf("alpha insert: %v", err)
		}
	})

	t.Run("beta_must_not_see_alpha", func(t *testing.T) {
		t.Parallel()
		<-alphaInserted
		pool := testutil.NewTestDB(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var count int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM users WHERE username = 'alpha'").Scan(&count); err != nil {
			t.Fatalf("beta query: %v", err)
		}
		if count != 0 {
			t.Fatalf("isolation broken: beta sees %d alpha-row(s)", count)
		}
	})
}
