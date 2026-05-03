package idempotency_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/repository/idempotency"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// makeUser inserts a row directly into users. The user repository (Phase 3.1)
// will own this work; for now the idempotency tests need a real user_id so the
// FK is valid.
func makeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'Test', $3, 'x')
	`, id, "u_"+id.String()[:8], id.String()+"@example.test")
	if err != nil {
		t.Fatalf("makeUser: %v", err)
	}
	return id
}

// hash32 returns a 32-byte SHA-256 of body so we satisfy the schema's
// octet_length(request_hash) = 32 CHECK constraint without thinking about it.
func hash32(body string) []byte {
	sum := sha256.Sum256([]byte(body))
	return sum[:]
}

func TestPut_ThenGet_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	want := idempotency.PutParams{
		Key:            "key-" + uuid.NewString(),
		UserID:         userID,
		RequestHash:    hash32("POST /v1/foo body=1"),
		ResponseStatus: 200,
		ResponseBody:   []byte(`{"ok":true}`),
		TTL:            1 * time.Hour,
	}
	put, err := repo.Put(ctx, want)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := repo.Get(ctx, want.Key, want.UserID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Key != want.Key || got.UserID != want.UserID {
		t.Errorf("identity mismatch: got %+v", got)
	}
	if !bytes.Equal(got.RequestHash, want.RequestHash) {
		t.Errorf("RequestHash mismatch")
	}
	if got.ResponseStatus != want.ResponseStatus {
		t.Errorf("ResponseStatus = %d, want %d", got.ResponseStatus, want.ResponseStatus)
	}
	if !bytes.Equal(got.ResponseBody, want.ResponseBody) {
		t.Errorf("ResponseBody mismatch")
	}
	if !got.ExpiresAt.Equal(put.ExpiresAt) {
		t.Errorf("ExpiresAt round-trip mismatch: get=%v put=%v", got.ExpiresAt, put.ExpiresAt)
	}
}

func TestPut_RoundTripsResponseHeaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	headers := map[string][]string{
		"Content-Type":  {"application/json"},
		"X-Custom-Hint": {"a", "b"}, // multi-value round-trip
	}
	want := idempotency.PutParams{
		Key: "key-h", UserID: userID,
		RequestHash:     hash32("POST /v1/foo body=1"),
		ResponseStatus:  201,
		ResponseHeaders: headers,
		ResponseBody:    []byte(`{"ok":true}`),
		TTL:             time.Hour,
	}
	if _, err := repo.Put(ctx, want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := repo.Get(ctx, want.Key, want.UserID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResponseHeaders["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type lost: %+v", got.ResponseHeaders)
	}
	xh := got.ResponseHeaders["X-Custom-Hint"]
	if len(xh) != 2 || xh[0] != "a" || xh[1] != "b" {
		t.Errorf("multi-value header lost order or values: %+v", xh)
	}
}

func TestReserve_FirstCallerWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	hash := hash32("POST /v1/x body")
	first, ok, err := repo.Reserve(ctx, idempotency.ReserveParams{
		Key: "rsv", UserID: userID, RequestHash: hash, TTL: time.Hour,
	})
	if err != nil || !ok {
		t.Fatalf("first Reserve: ok=%v err=%v", ok, err)
	}
	if first.ResponseStatus != idempotency.PlaceholderStatus {
		t.Errorf("placeholder status = %d, want %d", first.ResponseStatus, idempotency.PlaceholderStatus)
	}

	// Second caller: existing row returned, ok=false.
	existing, ok, err := repo.Reserve(ctx, idempotency.ReserveParams{
		Key: "rsv", UserID: userID, RequestHash: hash, TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if ok {
		t.Errorf("second caller must lose; ok=true")
	}
	if existing.Key != first.Key {
		t.Errorf("existing entry mismatch: %v vs %v", existing.Key, first.Key)
	}
}

func TestComplete_ReplacesPlaceholder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	if _, ok, err := repo.Reserve(ctx, idempotency.ReserveParams{
		Key: "c", UserID: userID, RequestHash: hash32("body"), TTL: time.Hour,
	}); err != nil || !ok {
		t.Fatalf("seed reserve: ok=%v err=%v", ok, err)
	}

	rows, err := repo.Complete(ctx, idempotency.CompleteParams{
		Key: "c", UserID: userID,
		ResponseStatus:  201,
		ResponseHeaders: map[string][]string{"Content-Type": {"application/json"}},
		ResponseBody:    []byte(`{"ok":true}`),
		TTL:             time.Hour,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if rows != 1 {
		t.Errorf("Complete should replace exactly 1 row, got %d", rows)
	}

	// Confirm: Get returns the completed entry.
	got, err := repo.Get(ctx, "c", userID)
	if err != nil {
		t.Fatalf("Get after Complete: %v", err)
	}
	if got.ResponseStatus != 201 {
		t.Errorf("status = %d, want 201", got.ResponseStatus)
	}
	if got.ResponseHeaders["Content-Type"][0] != "application/json" {
		t.Errorf("headers lost: %+v", got.ResponseHeaders)
	}
	if string(got.ResponseBody) != `{"ok":true}` {
		t.Errorf("body lost: %s", got.ResponseBody)
	}
}

func TestComplete_NoOpWhenNotPlaceholder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	if _, _, err := repo.Reserve(ctx, idempotency.ReserveParams{
		Key: "x", UserID: userID, RequestHash: hash32("a"), TTL: time.Hour,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := repo.Complete(ctx, idempotency.CompleteParams{
		Key: "x", UserID: userID, ResponseStatus: 200, ResponseBody: []byte("A"), TTL: time.Hour,
	}); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	// Second Complete: row is no longer a placeholder, so 0 rows
	// affected — protects against double-finalization.
	rows, err := repo.Complete(ctx, idempotency.CompleteParams{
		Key: "x", UserID: userID, ResponseStatus: 500, ResponseBody: []byte("B"), TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if rows != 0 {
		t.Errorf("second Complete must be a no-op, rowsAffected = %d", rows)
	}
}

func TestDeleteByKey_RemovesPlaceholder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	if _, _, err := repo.Reserve(ctx, idempotency.ReserveParams{
		Key: "d", UserID: userID, RequestHash: hash32("a"), TTL: time.Hour,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rows, err := repo.DeleteByKey(ctx, "d", userID)
	if err != nil {
		t.Fatalf("DeleteByKey: %v", err)
	}
	if rows != 1 {
		t.Errorf("expected 1 row deleted, got %d", rows)
	}
	// After delete, a fresh Reserve should succeed (no stale placeholder).
	if _, ok, err := repo.Reserve(ctx, idempotency.ReserveParams{
		Key: "d", UserID: userID, RequestHash: hash32("b"), TTL: time.Hour,
	}); err != nil || !ok {
		t.Errorf("post-delete Reserve should succeed: ok=%v err=%v", ok, err)
	}
}

func TestPut_ConflictReturnsErrConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	first := idempotency.PutParams{
		Key: "race-key", UserID: userID,
		RequestHash: hash32("a"), ResponseStatus: 200,
		ResponseBody: []byte("A"), TTL: time.Hour,
	}
	if _, err := repo.Put(ctx, first); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	second := first
	second.RequestHash = hash32("b")
	second.ResponseBody = []byte("B")
	if _, err := repo.Put(ctx, second); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate (key, user_id), got %v", err)
	}
}

func TestGet_MissReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	_, err := repo.Get(ctx, "never-stored", userID)
	if !errors.Is(err, idempotency.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// "Hash mismatch" is a service-level concern (the middleware compares hashes),
// so the repo's job is just to faithfully return the stored bytes. This test
// proves Get returns the stored RequestHash unchanged so the caller can do
// the comparison.
func TestGet_HashMismatchVisibleToCaller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	storedHash := hash32("body-A")
	_, err := repo.Put(ctx, idempotency.PutParams{
		Key:            "k1",
		UserID:         userID,
		RequestHash:    storedHash,
		ResponseStatus: 201,
		ResponseBody:   []byte("A"),
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := repo.Get(ctx, "k1", userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	differentHash := hash32("body-B")
	if bytes.Equal(got.RequestHash, differentHash) {
		t.Fatal("test setup wrong: hashes happen to match")
	}
	if !bytes.Equal(got.RequestHash, storedHash) {
		t.Fatal("Get returned a hash that doesn't match what we stored")
	}
}

func TestGet_TTLExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	// Put with the smallest acceptable TTL, then sleep past it. The Get must
	// treat an expired row as a miss (the WHERE clause filters expired rows).
	_, err := repo.Put(ctx, idempotency.PutParams{
		Key:            "expiring",
		UserID:         userID,
		RequestHash:    hash32("x"),
		ResponseStatus: 200,
		ResponseBody:   []byte{},
		TTL:            50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	_, err = repo.Get(ctx, "expiring", userID)
	if !errors.Is(err, idempotency.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for expired row, got %v", err)
	}

	// DeleteExpired should then physically remove the row. We confirm by
	// asserting count via the pool directly (bypassing Get's expires_at filter).
	_, err = repo.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM idempotency_keys WHERE user_id = $1 AND key = 'expiring'",
		userID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("DeleteExpired left %d expired rows", n)
	}
}

func TestPut_CascadesOnUserDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	repo := idempotency.New(pool)
	userID := makeUser(ctx, t, pool)

	_, err := repo.Put(ctx, idempotency.PutParams{
		Key: "k", UserID: userID,
		RequestHash:    hash32("x"),
		ResponseStatus: 200, ResponseBody: []byte{},
		TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Hard-delete the user. The FK is ON DELETE CASCADE so the idempotency
	// row should disappear too. (V1 never hard-deletes users in production —
	// it soft-deletes — but the FK behavior is what the schema promises.)
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
		t.Fatalf("DELETE user: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM idempotency_keys WHERE user_id = $1", userID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("ON DELETE CASCADE did not fire: %d rows remain", n)
	}
}

// Spec-mandated assertion (§16 milestone 1.8): the set_updated_at() trigger
// from migration 0001 advances updated_at on UPDATE. Using `users` as the proof
// since milestone 1.8 explicitly calls it out.
func TestSetUpdatedAtTrigger_AdvancesOnUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testutil.NewTestDB(t)
	userID := makeUser(ctx, t, pool)

	var before time.Time
	if err := pool.QueryRow(ctx,
		"SELECT updated_at FROM users WHERE id = $1", userID,
	).Scan(&before); err != nil {
		t.Fatalf("read before: %v", err)
	}

	// A short sleep is needed because postgres' now() is constant within a
	// transaction at sub-microsecond resolution — without the gap the trigger
	// would set updated_at to a value identical to created_at.
	time.Sleep(10 * time.Millisecond)

	if _, err := pool.Exec(ctx,
		"UPDATE users SET display_name = 'Renamed' WHERE id = $1", userID,
	); err != nil {
		t.Fatalf("update: %v", err)
	}

	var after time.Time
	if err := pool.QueryRow(ctx,
		"SELECT updated_at FROM users WHERE id = $1", userID,
	).Scan(&after); err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !after.After(before) {
		t.Fatalf("trigger did not advance updated_at: before=%v after=%v", before, after)
	}
}
