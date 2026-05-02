package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
	"github.com/cadenlund/wakeup/apps/backend/internal/ratelimit"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// realLimiter spins up the testcontainer Redis singleton + a unique
// keyspace per call so parallel tests don't share counters.
func realLimiter(t *testing.T) *ratelimit.Limiter {
	t.Helper()
	url := testutil.StartRedis(t)
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	return ratelimit.New(client)
}

func TestRateLimit_AllowsUnderLimit(t *testing.T) {
	t.Parallel()
	cfg := mw.RateLimitConfig{
		Limiter: realLimiter(t),
		Scope:   "test-allow-" + testutil.NextSuffix(),
		Limit:   3,
		Window:  time.Minute,
	}
	called := 0
	h := mw.RateLimit(cfg, fakeWriteError)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("req %d status = %d, want 200", i, rec.Code)
		}
	}
	if called != 3 {
		t.Errorf("downstream called %d times, want 3", called)
	}
}

func TestRateLimit_RejectsOverLimit(t *testing.T) {
	t.Parallel()
	cfg := mw.RateLimitConfig{
		Limiter: realLimiter(t),
		Scope:   "test-reject-" + testutil.NextSuffix(),
		Limit:   2,
		Window:  time.Minute,
	}
	h := mw.RateLimit(cfg, fakeWriteError)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.2:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:1234"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd req status = %d, want 429", rec.Code)
	}

	var env struct {
		Error struct {
			Code              string `json:"code"`
			RetryAfterSeconds int    `json:"retry_after_seconds"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != string(apierror.CodeRateLimited) {
		t.Errorf("error.code = %q, want RATE_LIMITED", env.Error.Code)
	}
}

func TestRateLimit_AuthedUserKeyedByID(t *testing.T) {
	t.Parallel()
	cfg := mw.RateLimitConfig{
		Limiter: realLimiter(t),
		Scope:   "test-auth-" + testutil.NextSuffix(),
		Limit:   1,
		Window:  time.Minute,
	}
	h := mw.RateLimit(cfg, fakeWriteError)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	user1 := &domain.User{ID: uuid.New(), Role: "user"}
	user2 := &domain.User{ID: uuid.New(), Role: "user"}

	// Each user should get their own bucket — running through twice
	// proves they don't share state.
	for _, u := range []*domain.User{user1, user2} {
		req := httptest.NewRequest(http.MethodGet, "/", nil).
			WithContext(mw.WithUser(context.Background(), u))
		req.RemoteAddr = "127.0.0.1:0"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("first req for %v: status = %d, want 200", u.ID, rec.Code)
		}
	}
	// user1 should now be over-budget.
	req := httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(mw.WithUser(context.Background(), user1))
	req.RemoteAddr = "127.0.0.1:0"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("user1 second req status = %d, want 429", rec.Code)
	}
}
