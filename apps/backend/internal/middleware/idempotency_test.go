package middleware_test

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
	idemrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/idempotency"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// idemFixture is the per-test pgtestdb-backed setup. Per CodeRabbit on
// PR #74, the middleware tests now exercise the REAL idempotency
// repository against a fresh per-test database, not a custom in-memory
// double — so any divergence between the middleware's expectations and
// the production repo (NULL handling, header round-trip, ErrConflict
// semantics) shows up here.
type idemFixture struct {
	pool *pgxpool.Pool
	repo *idemrepo.Queries
}

func newIdemFixture(t *testing.T) *idemFixture {
	t.Helper()
	pool := testutil.NewTestDB(t)
	return &idemFixture{pool: pool, repo: idemrepo.New(pool)}
}

// makeIdemUser inserts a user via raw SQL so the FK from
// idempotency_keys.user_id is satisfied. Same trick used in the repo's
// own tests; avoids dragging in the user repository's fixtures.
func (f *idemFixture) makeIdemUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	full := strings.ReplaceAll(id.String(), "-", "")
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO users (id, username, display_name, email, password_hash)
		VALUES ($1, $2, 'T', $3, 'h')
	`, id, "u"+full, full+"@x.test")
	if err != nil {
		t.Fatalf("makeIdemUser: %v", err)
	}
	return id
}

// countEntries returns the number of live (unexpired) rows for the
// given user — replaces the old memStore.entries length checks.
func (f *idemFixture) countEntries(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM idempotency_keys WHERE user_id = $1 AND expires_at > now()",
		userID,
	).Scan(&n); err != nil {
		t.Fatalf("countEntries: %v", err)
	}
	return n
}

func ctxWithUser(uid uuid.UUID) context.Context {
	u := &domain.User{ID: uid, Role: "user"}
	return mw.WithUser(context.Background(), u)
}

func newIdempotencyStack(t *testing.T, store mw.IdempotencyStore, downstream http.HandlerFunc) http.Handler {
	t.Helper()
	return mw.Idempotency(mw.IdempotencyConfig{
		Store: store, WriteError: fakeWriteError,
	})(downstream)
}

func TestIdempotency_NoHeaderPassesThrough(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	called := 0
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader("body")).
		WithContext(ctxWithUser(uid))
	h.ServeHTTP(rec, req)
	if called != 1 {
		t.Errorf("downstream calls = %d, want 1", called)
	}
	if rec.Header().Get(mw.IdempotentReplayHeader) != "" {
		t.Errorf("Idempotent-Replay should be absent without a key, got %q",
			rec.Header().Get(mw.IdempotentReplayHeader))
	}
}

func TestIdempotency_NonWriteMethodPassesThrough(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	called := 0
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil).WithContext(ctxWithUser(uid))
	req.Header.Set(mw.IdempotencyKeyHeader, "anything")
	h.ServeHTTP(rec, req)
	if called != 1 {
		t.Errorf("GET should have passed through, got %d calls", called)
	}
}

func TestIdempotency_FreshRequestCachesAndStreams(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	called := 0
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"a":1}`)).
		WithContext(ctxWithUser(uid))
	req.Header.Set(mw.IdempotencyKeyHeader, "key-fresh")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get(mw.IdempotentReplayHeader); got != "false" {
		t.Errorf("first-time replay header = %q, want false", got)
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("body lost: %s", rec.Body.String())
	}
	if called != 1 {
		t.Errorf("downstream calls = %d, want 1", called)
	}
	if n := f.countEntries(t, uid); n != 1 {
		t.Errorf("expected 1 cached entry, got %d", n)
	}
}

func TestIdempotency_ReplayServesCachedResponse(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	called := 0
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"first":true}`))
	})
	first := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"a":1}`)).
			WithContext(ctxWithUser(uid))
		req.Header.Set(mw.IdempotencyKeyHeader, "key-replay")
		h.ServeHTTP(rec, req)
		return rec
	}()
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d", first.Code)
	}

	// Same key + same body → cache hit; handler MUST NOT be called again.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"a":1}`)).
		WithContext(ctxWithUser(uid))
	req.Header.Set(mw.IdempotencyKeyHeader, "key-replay")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("replayed status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get(mw.IdempotentReplayHeader); got != "true" {
		t.Errorf("Idempotent-Replay = %q, want true", got)
	}
	if rec.Body.String() != `{"first":true}` {
		t.Errorf("replayed body mismatch: %s", rec.Body.String())
	}
	if called != 1 {
		t.Errorf("downstream calls = %d, want 1 (replay must skip)", called)
	}
}

func TestIdempotency_KeyReusedDifferentBodyReturns422(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	// Seed via the wire with body A.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"a":1}`)).
		WithContext(ctxWithUser(uid))
	req1.Header.Set(mw.IdempotencyKeyHeader, "key-conflict")
	h.ServeHTTP(rec1, req1)

	// Same key + different body → 422.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"a":2}`)).
		WithContext(ctxWithUser(uid))
	req.Header.Set(mw.IdempotencyKeyHeader, "key-conflict")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), string(apierror.CodeIdempotencyKeyReused)) {
		t.Errorf("body should contain IDEMPOTENCY_KEY_REUSED: %s", rec.Body.String())
	}
}

func TestIdempotency_KeysScopedPerUser(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	called := 0
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	// Two different users using the same key string. Both must run the
	// handler — one user's cache hit must NOT replay for the other.
	for _, uid := range []uuid.UUID{f.makeIdemUser(t), f.makeIdemUser(t)} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)).
			WithContext(ctxWithUser(uid))
		req.Header.Set(mw.IdempotencyKeyHeader, "shared-key")
		h.ServeHTTP(rec, req)
	}
	if called != 2 {
		t.Errorf("downstream calls = %d, want 2 (per-user scoping)", called)
	}
}

func TestIdempotency_5xxNotCached(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	called := 0
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		called++
		// Force a 500 response.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("transient"))
	})
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)).
			WithContext(ctxWithUser(uid))
		req.Header.Set(mw.IdempotencyKeyHeader, "key-5xx")
		h.ServeHTTP(rec, req)
	}
	if called != 2 {
		t.Errorf("5xx must not be cached; downstream calls = %d, want 2", called)
	}
	if n := f.countEntries(t, uid); n != 0 {
		t.Errorf("5xx must not be cached; rows = %d", n)
	}
}

func TestIdempotency_BodyTooLargeSkipsAndPassesThrough(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	big := strings.Repeat("x", mw.MaxIdempotentBodyBytes+1)
	called := 0
	var handlerSawBody []byte
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, r *http.Request) {
		called++
		// Read the full body so the test catches the regression where
		// the middleware truncated to MaxIdempotentBodyBytes+1 instead
		// of reattaching the remaining bytes via MultiReader.
		read, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("downstream read body: %v", err)
		}
		handlerSawBody = read
		w.WriteHeader(http.StatusCreated)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(big)).
		WithContext(ctxWithUser(uid))
	req.Header.Set(mw.IdempotencyKeyHeader, "key-big")
	h.ServeHTTP(rec, req)
	if rec.Header().Get(mw.IdempotentReplayHeader) != "skipped" {
		t.Errorf("Idempotent-Replay = %q, want skipped",
			rec.Header().Get(mw.IdempotentReplayHeader))
	}
	if called != 1 {
		t.Errorf("downstream should still run on body-too-large, got %d calls", called)
	}
	if string(handlerSawBody) != big {
		t.Errorf("oversized body truncated: handler saw %d bytes, want %d",
			len(handlerSawBody), len(big))
	}
	if n := f.countEntries(t, uid); n != 0 {
		t.Errorf("body-too-large must not be cached; rows = %d", n)
	}
}

func TestIdempotency_NoUserReturns401(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`))
	req.Header.Set(mw.IdempotencyKeyHeader, "no-user")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// Replay must restore the response headers the handler set on the
// first call. Without this, the cached body would surface with a
// default Content-Type and clients would parse JSON as text/plain.
func TestIdempotency_ReplayRestoresHeaders(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Custom", "first-call")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"x":1}`))
	})
	first := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)).
		WithContext(ctxWithUser(uid))
	req1.Header.Set(mw.IdempotencyKeyHeader, "key-headers")
	h.ServeHTTP(first, req1)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d", first.Code)
	}

	// Second request: cached replay. Headers must round-trip.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)).
		WithContext(ctxWithUser(uid))
	req.Header.Set(mw.IdempotencyKeyHeader, "key-headers")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("replayed Content-Type = %q, want json", got)
	}
	if got := rec.Header().Get("X-Custom"); got != "first-call" {
		t.Errorf("replayed X-Custom = %q, want first-call", got)
	}
	if got := rec.Header().Get(mw.IdempotentReplayHeader); got != "true" {
		t.Errorf("Idempotent-Replay = %q, want true", got)
	}
}

// Atomic reservation: the second concurrent request with the same
// (user_id, key) must NOT run the handler. Reserve loses the race,
// finds the in-flight placeholder, and surfaces 422 IDEMPOTENCY_KEY_REUSED
// — which is the contract for "another request is currently processing
// this key." Without the reservation primitive, both requests would have
// run handlers and produced duplicate side effects (the original race
// CodeRabbit raised on PR #74).
func TestIdempotency_ReservationBlocksConcurrentHandler(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)

	// Pre-seed an in-flight placeholder by calling Reserve directly —
	// equivalent to "another goroutine just reserved 100ms ago and is
	// running the handler". The next request through the middleware
	// will trip the reservation conflict path.
	hash := requestHashHelper(http.MethodPost, "/v1/x", []byte(`{"a":1}`))
	if _, ok, err := f.repo.Reserve(context.Background(), idemrepo.ReserveParams{
		Key: "race-key", UserID: uid, RequestHash: hash, TTL: time.Hour,
	}); err != nil || !ok {
		t.Fatalf("seed reservation: ok=%v err=%v", ok, err)
	}

	called := 0
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"second":true}`))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"a":1}`)).
		WithContext(ctxWithUser(uid))
	req.Header.Set(mw.IdempotencyKeyHeader, "race-key")
	h.ServeHTTP(rec, req)

	if called != 0 {
		t.Errorf("handler MUST NOT run when another reservation is in flight, calls = %d", called)
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), string(apierror.CodeIdempotencyKeyReused)) {
		t.Errorf("body should contain IDEMPOTENCY_KEY_REUSED, got %s", rec.Body.String())
	}
}

// 5xx path: the placeholder must be removed so a client retry isn't
// blocked by a stale in-flight reservation. (§4.8 says don't cache 5xx.)
func TestIdempotency_5xxClearsPlaceholder(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	called := 0
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("transient"))
	})

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)).
			WithContext(ctxWithUser(uid))
		req.Header.Set(mw.IdempotencyKeyHeader, "key-5xx-clears")
		h.ServeHTTP(rec, req)
	}
	if called != 2 {
		t.Errorf("5xx must NOT block retries via stale placeholder; downstream calls = %d, want 2", called)
	}
	if n := f.countEntries(t, uid); n != 0 {
		t.Errorf("placeholder should be cleared on 5xx; rows = %d", n)
	}
}

// captureWriter must give the handler its OWN header map, not the
// underlying writer's. Otherwise headers set by upstream middleware
// (X-Request-ID, CORS, Set-Cookie from session middleware, etc.)
// would be snapshotted into the cache entry and replayed on every
// future request — duplicating per-request metadata or poisoning
// future responses with stale values. (CodeRabbit caught this on PR #74.)
func TestIdempotency_DoesNotSnapshotUpstreamHeaders(t *testing.T) {
	t.Parallel()
	f := newIdemFixture(t)
	uid := f.makeIdemUser(t)
	h := newIdempotencyStack(t, f.repo, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Handler-Set", "yes")
		w.WriteHeader(http.StatusCreated)
	})
	// Wrap with a fake "upstream middleware" that sets a per-request
	// header on the response BEFORE the idempotency middleware sees it.
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Set", "request-1")
		h.ServeHTTP(w, r)
	})

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)).
		WithContext(ctxWithUser(uid))
	req1.Header.Set(mw.IdempotencyKeyHeader, "key-no-pollution")
	upstream.ServeHTTP(rec1, req1)

	// Cached entry — fetched via the real repo — must NOT contain the
	// upstream header.
	cached, err := f.repo.Get(context.Background(), "key-no-pollution", uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, leaked := cached.ResponseHeaders["X-Upstream-Set"]; leaked {
		t.Errorf("upstream header leaked into cache: %+v", cached.ResponseHeaders)
	}
	if cached.ResponseHeaders["X-Handler-Set"][0] != "yes" {
		t.Errorf("handler header missing from cache: %+v", cached.ResponseHeaders)
	}
}

func TestIdempotency_PanicsOnBadConfig(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil Store")
		}
	}()
	_ = mw.Idempotency(mw.IdempotencyConfig{WriteError: fakeWriteError})
}

// requestHashHelper duplicates the unexported requestHash so tests can
// pre-seed the repo with the exact bytes the middleware will compute.
func requestHashHelper(method, path string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte(" "))
	h.Write([]byte(path))
	h.Write([]byte("\n"))
	h.Write(body)
	return h.Sum(nil)
}
