// Package ratelimit is the Redis-backed sliding-window limiter the
// middleware applies per §8.3. The §4.7 chain calls Allow on each request;
// when allowed=false, the response is rendered via apierror.RateLimited(retry).
//
// Algorithm — sliding window log via a sorted set, atomic Lua script:
//
//  1. ZREMRANGEBYSCORE key 0 (now - window)   // drop entries outside the window
//  2. count = ZCARD key                        // active entries in the window
//  3. if count >= limit: return (allowed=false, retry = (oldest + window) - now)
//  4. ZADD key now <unique-member>             // log this request
//     PEXPIRE key window                       // bound key lifetime
//     return (allowed=true, retry=0)
//
// Lua keeps the read/check/log critical section atomic, so two concurrent
// requests can't both squeak past the limit because of an interleaved CHECK.
package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter is the §8.3 rate limiter. Cheap to construct and goroutine-safe;
// share one *Limiter for the whole process.
type Limiter struct {
	client *redis.Client
	now    func() time.Time // overridable for deterministic tests
}

// New returns a Limiter backed by client. The caller owns the client's
// lifecycle.
func New(client *redis.Client) *Limiter {
	if client == nil {
		panic("ratelimit: New called with nil client")
	}
	return &Limiter{client: client, now: time.Now}
}

// BuildKey concatenates scope + identifier into the §8.3 redis-key shape.
// Pass scope = the route group ("auth" / "writes" / "reads" / "ws-send")
// and identifier = the user_id or IP address.
func BuildKey(scope, identifier string) string {
	return "rl:" + scope + ":" + identifier
}

// Allow reports whether a request keyed at key is permitted under
// (limit, window). On the deny path, retryAfter is the time until the
// oldest log entry rotates out of the window — the caller surfaces that
// to apierror.RateLimited and the Retry-After header.
//
// Errors are wrapped with package context. A redis transport failure
// returns the error AND allowed=true (fail-open) — better to let traffic
// through than reject every request when the limiter itself is down. This
// is the standard fault-tolerant choice for an online-API rate limiter
// (Kong, Cloudflare, etc. all behave this way).
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	if limit <= 0 {
		return false, 0, fmt.Errorf("ratelimit: limit must be > 0, got %d", limit)
	}
	if window <= 0 {
		return false, 0, fmt.Errorf("ratelimit: window must be > 0, got %s", window)
	}
	if strings.TrimSpace(key) == "" {
		return false, 0, errors.New("ratelimit: key is empty")
	}

	nowMs := l.now().UnixMilli()
	windowMs := windowMillisCeil(window)

	// Random unique member so concurrent requests within the same ms don't
	// collide on score collisions in the sorted set.
	member, err := uniqueMember()
	if err != nil {
		// Cryptographic randomness can't fail in practice; fail-open if it does.
		return true, 0, fmt.Errorf("ratelimit: uniqueMember: %w", err)
	}

	res, scriptErr := allowScript.Run(ctx, l.client, []string{key},
		nowMs, windowMs, limit, member,
	).Result()
	if scriptErr != nil {
		// Fail-open: redis is down, don't reject every request.
		return true, 0, fmt.Errorf("ratelimit: redis allow: %w", scriptErr)
	}

	parsed, ok := res.([]any)
	if !ok || len(parsed) != 2 {
		return true, 0, fmt.Errorf("ratelimit: unexpected lua result shape: %T %v", res, res)
	}
	allowedFlag, _ := parsed[0].(int64)
	retryMs, _ := parsed[1].(int64)
	if allowedFlag == 1 {
		return true, 0, nil
	}
	if retryMs <= 0 {
		retryMs = 1
	}
	return false, time.Duration(retryMs) * time.Millisecond, nil
}

// uniqueMember returns a 16-hex-char random token (8 bytes of entropy) used
// as the sorted-set member so concurrent same-millisecond requests don't
// share a member name.
func uniqueMember() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// windowMillisCeil rounds the window UP to at least 1ms. time.Duration.
// Milliseconds() truncates sub-millisecond durations to 0, which would make
// Redis PEXPIRE 0 the bucket key immediately and let traffic past the limit.
// Ceiling division preserves the limiter's contract for any positive window.
//
// Exported for tests; not part of the public surface a caller should use.
func windowMillisCeil(window time.Duration) int64 {
	if window <= 0 {
		return 0
	}
	return int64((window + time.Millisecond - 1) / time.Millisecond)
}

// allowScript is the atomic check-and-log primitive. KEYS[1] = bucket key.
// ARGV: 1=now-ms, 2=window-ms, 3=limit, 4=unique member.
// Returns {1, 0} when allowed, {0, retry-ms} when denied.
var allowScript = redis.NewScript(`
local key      = KEYS[1]
local now      = tonumber(ARGV[1])
local windowMs = tonumber(ARGV[2])
local limit    = tonumber(ARGV[3])
local member   = ARGV[4]

-- Drop entries that fell out of the window.
redis.call("ZREMRANGEBYSCORE", key, 0, now - windowMs)
local count = redis.call("ZCARD", key)

if count >= limit then
  local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
  local retry  = (tonumber(oldest[2]) + windowMs) - now
  if retry < 1 then retry = 1 end
  return {0, retry}
end

redis.call("ZADD", key, now, member)
redis.call("PEXPIRE", key, windowMs)
return {1, 0}
`)
