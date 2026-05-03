package middleware_test

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
	idemrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/idempotency"
)

// memStore is an in-memory IdempotencyStore. Lets the middleware tests
// run without a real DB while exercising every branch of the §4.8 flow.
type memStore struct {
	mu      sync.Mutex
	entries map[string]idemrepo.Entry // key=key+userID
}

func newMemStore() *memStore { return &memStore{entries: make(map[string]idemrepo.Entry)} }

func (m *memStore) k(key string, uid uuid.UUID) string { return key + "::" + uid.String() }

func (m *memStore) Get(_ context.Context, key string, userID uuid.UUID) (idemrepo.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[m.k(key, userID)]; ok {
		return e, nil
	}
	return idemrepo.Entry{}, idemrepo.ErrNotFound
}

func (m *memStore) Put(_ context.Context, p idemrepo.PutParams) (idemrepo.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[m.k(p.Key, p.UserID)]; ok {
		return idemrepo.Entry{}, idemrepo.ErrConflict
	}
	e := idemrepo.Entry{
		Key: p.Key, UserID: p.UserID, RequestHash: p.RequestHash,
		ResponseStatus:  p.ResponseStatus,
		ResponseHeaders: p.ResponseHeaders,
		ResponseBody:    p.ResponseBody,
	}
	m.entries[m.k(p.Key, p.UserID)] = e
	return e, nil
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
	store := newMemStore()
	called := 0
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader("body")).
		WithContext(ctxWithUser(uuid.New()))
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
	store := newMemStore()
	called := 0
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil).WithContext(ctxWithUser(uuid.New()))
	req.Header.Set(mw.IdempotencyKeyHeader, "anything")
	h.ServeHTTP(rec, req)
	if called != 1 {
		t.Errorf("GET should have passed through, got %d calls", called)
	}
}

func TestIdempotency_FreshRequestCachesAndStreams(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	called := 0
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"a":1}`)).
		WithContext(ctxWithUser(uuid.New()))
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
	if len(store.entries) != 1 {
		t.Errorf("expected 1 cached entry, got %d", len(store.entries))
	}
}

func TestIdempotency_ReplayServesCachedResponse(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	called := 0
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"first":true}`))
	})
	uid := uuid.New()
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
	store := newMemStore()
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	uid := uuid.New()
	// Seed the store with body A.
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
	store := newMemStore()
	called := 0
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	// Two different users using the same key string. Both must run the
	// handler — one user's cache hit must NOT replay for the other.
	for _, uid := range []uuid.UUID{uuid.New(), uuid.New()} {
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
	store := newMemStore()
	called := 0
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		called++
		// Force a 500 response.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("transient"))
	})
	uid := uuid.New()
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
}

func TestIdempotency_BodyTooLargeSkipsAndPassesThrough(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	called := 0
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
	})
	big := strings.Repeat("x", mw.MaxIdempotentBodyBytes+1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(big)).
		WithContext(ctxWithUser(uuid.New()))
	req.Header.Set(mw.IdempotencyKeyHeader, "key-big")
	h.ServeHTTP(rec, req)
	if rec.Header().Get(mw.IdempotentReplayHeader) != "skipped" {
		t.Errorf("Idempotent-Replay = %q, want skipped",
			rec.Header().Get(mw.IdempotentReplayHeader))
	}
	if called != 1 {
		t.Errorf("downstream should still run on body-too-large, got %d calls", called)
	}
	if len(store.entries) != 0 {
		t.Errorf("body-too-large must not be cached")
	}
}

func TestIdempotency_NoUserReturns401(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
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
	store := newMemStore()
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Custom", "first-call")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"x":1}`))
	})
	uid := uuid.New()
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

// Concurrent writers race: simulate two requests with the same key
// where the *second* Put hits a PK conflict. The middleware must log
// the race and not corrupt the in-flight response (the first request
// already wrote one). We verify the Idempotent-Replay header on the
// racing request stays "false" since the handler did execute.
func TestIdempotency_PutConflictDoesNotFailRequest(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	uid := uuid.New()

	// Pre-seed an entry so the next Put trips ErrConflict.
	if _, err := store.Put(context.Background(), idemrepo.PutParams{
		Key: "race-key", UserID: uid,
		RequestHash:    requestHashHelper("POST", "/v1/x", []byte(`{"a":1}`)),
		ResponseStatus: http.StatusCreated,
		ResponseBody:   []byte(`{"first":true}`),
		TTL:            time.Hour,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// stub Get to miss so the middleware falls into the Put path
	// and trips ErrConflict on the way out.
	racing := &raceStore{base: store}
	called := 0
	h := mw.Idempotency(mw.IdempotencyConfig{
		Store: racing, WriteError: fakeWriteError,
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"second":true}`))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"a":1}`)).
		WithContext(ctxWithUser(uid))
	req.Header.Set(mw.IdempotencyKeyHeader, "race-key")
	h.ServeHTTP(rec, req)

	if called != 1 {
		t.Errorf("handler should have run despite race, calls = %d", called)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get(mw.IdempotentReplayHeader); got != "false" {
		t.Errorf("racing request is fresh, Idempotent-Replay = %q, want false", got)
	}
}

// raceStore returns ErrNotFound on Get even when the underlying memStore
// has the key — simulates a race where two goroutines both miss Get
// before either has written. Put delegates to the base so the
// pre-seeded conflict surfaces normally.
type raceStore struct{ base *memStore }

func (r *raceStore) Get(_ context.Context, _ string, _ uuid.UUID) (idemrepo.Entry, error) {
	return idemrepo.Entry{}, idemrepo.ErrNotFound
}

func (r *raceStore) Put(ctx context.Context, p idemrepo.PutParams) (idemrepo.Entry, error) {
	return r.base.Put(ctx, p)
}

// requestHashHelper duplicates the unexported requestHash so tests can
// pre-seed the memStore with the exact bytes the middleware will compute.
func requestHashHelper(method, path string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte(" "))
	h.Write([]byte(path))
	h.Write([]byte("\n"))
	h.Write(body)
	return h.Sum(nil)
}

// captureWriter must give the handler its OWN header map, not the
// underlying writer's. Otherwise headers set by upstream middleware
// (X-Request-ID, CORS, Set-Cookie from session middleware, etc.)
// would be snapshotted into the cache entry and replayed on every
// future request — duplicating per-request metadata or poisoning
// future responses with stale values. (CodeRabbit caught this on PR #74.)
func TestIdempotency_DoesNotSnapshotUpstreamHeaders(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	uid := uuid.New()
	h := newIdempotencyStack(t, store, func(w http.ResponseWriter, _ *http.Request) {
		// Handler sets ONLY this one header.
		w.Header().Set("X-Handler-Set", "yes")
		w.WriteHeader(http.StatusCreated)
	})
	// Wrap with a fake "upstream middleware" that sets a per-request
	// header on the response BEFORE the idempotency middleware sees it.
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Set", "request-1")
		h.ServeHTTP(w, r)
	})

	// First request — caches.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{}`)).
		WithContext(ctxWithUser(uid))
	req1.Header.Set(mw.IdempotencyKeyHeader, "key-no-pollution")
	upstream.ServeHTTP(rec1, req1)

	// Cached entry must NOT contain the upstream header.
	cached := store.entries[store.k("key-no-pollution", uid)]
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
