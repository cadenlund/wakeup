package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/ratelimit"
)

// RateLimitConfig parameterizes a single instance of the §8.3 rate limit
// middleware. Pass one of these per route group (auth / writes / reads /
// ws-send) — different scopes have different limits.
type RateLimitConfig struct {
	// Limiter is the redis-backed sliding window. Required.
	Limiter *ratelimit.Limiter
	// Scope is the §8.3 scope tag baked into the redis key
	// ("auth", "writes", "reads", "ws-send").
	Scope string
	// Limit is the max events per Window.
	Limit int
	// Window is the duration over which Limit applies.
	Window time.Duration
	// Logger receives non-fatal limiter errors. Optional.
	Logger *slog.Logger
}

// RateLimit returns middleware that consults cfg.Limiter and rejects
// over-budget requests with apierror.RateLimited. Identifier is
// `user_id` when the request is authed (LoadUser ran), else the
// remote IP. Limiter transport errors are fail-open per §8.3.
//
// writeError is required so the 429 response always uses the §4.4
// envelope shape — no plaintext fallback path.
func RateLimit(cfg RateLimitConfig, writeError errorWriter) func(http.Handler) http.Handler {
	if writeError == nil {
		panic("middleware.RateLimit: nil writeError")
	}
	if cfg.Limiter == nil {
		panic("middleware.RateLimit: nil Limiter")
	}
	if strings.TrimSpace(cfg.Scope) == "" {
		panic("middleware.RateLimit: empty Scope")
	}
	if cfg.Limit <= 0 {
		panic("middleware.RateLimit: Limit must be > 0")
	}
	if cfg.Window <= 0 {
		panic("middleware.RateLimit: Window must be > 0")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ident := identifier(r)
			key := ratelimit.BuildKey(cfg.Scope, ident)
			allowed, retryAfter, err := cfg.Limiter.Allow(r.Context(), key, cfg.Limit, cfg.Window)
			if err != nil {
				logger.WarnContext(r.Context(), "ratelimit error",
					slog.String("scope", cfg.Scope),
					slog.String("identifier", ident),
					slog.String("error", err.Error()),
				)
				// Fail-open per §8.3 — better to let traffic through than
				// reject every request when the limiter itself is down.
				// Skip the !allowed branch entirely so a transport error
				// can't surface as a 429 (CodeRabbit caught the bug here).
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				writeError(w, r, apierror.RateLimited(retryAfterSeconds(retryAfter)))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// identifier returns user_id when the request is authed (LoadUser ran),
// otherwise the client IP. The IP is taken from RemoteAddr; reverse-proxy
// X-Forwarded-For parsing is the proxy's job to terminate per §8.3 — we
// trust whatever fronts us in prod.
func identifier(r *http.Request) string {
	if u := UserFromContext(r.Context()); u != nil {
		return "user:" + u.ID.String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might already be a bare IP (rare). Fall back to whole.
		host = r.RemoteAddr
	}
	if host == "" {
		host = "unknown"
	}
	return "ip:" + host
}

// retryAfterSeconds rounds up so a 1ms rejection still produces a 1s
// Retry-After (sub-second values aren't useful to clients).
func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	secs := int(d / time.Second)
	if d%time.Second != 0 {
		secs++
	}
	return secs
}
