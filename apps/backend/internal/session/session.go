// Package session wraps github.com/alexedwards/scs/v2 + pgxstore with the
// project-locked cookie configuration from WAKEUP.md §8.2. Every request goes
// through scs.SessionManager.LoadAndSave at the router root; this package is
// the only place the cookie name, lifetime, and security flags are configured
// so a future tweak is a one-line edit.
//
// Locked invariants (§8.2):
//   - Cookies only. NO Bearer-token / JWT adapter — neither web nor mobile.
//   - SameSite=Lax + Secure + HttpOnly = the only CSRF protection v1 ships.
//   - Lifetime 30d, IdleTimeout 7d.
package session

import (
	"net/http"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CookieName is the wire identity of the session cookie. Exported so middleware
// (e.g. logout / impersonation guard) can find / clear it without re-deriving.
const CookieName = "wakeup_session"

// Options tweaks the manager produced by New. Production callers should
// pass the zero value (Cookie.Secure=true). Local dev / smoke tests need
// SecureOverride=Insecure so the browser will round-trip cookies over
// plain-HTTP — §8.2 keeps Secure ON in production where TLS terminates
// upstream, but `just dev` is HTTP-only.
type Options struct {
	// Insecure: when true, set Cookie.Secure=false. Use only in local /
	// test environments. Production MUST leave this false (the spec
	// locks SameSite=Lax + Secure + HttpOnly as the v1 CSRF stance).
	Insecure bool
}

// New returns a scs.SessionManager backed by the provided pgxpool. The
// `sessions` table created by migration 0002 holds (token, data, expiry)
// in the schema pgxstore expects.
//
// The returned manager is goroutine-safe and intended to be a long-lived
// singleton — call once in cmd/server/main.go and reuse for every request.
func New(pool *pgxpool.Pool, opts ...Options) *scs.SessionManager {
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}

	m := scs.New()
	m.Store = pgxstore.New(pool)

	m.Lifetime = 30 * 24 * time.Hour   // 30-day absolute lifetime
	m.IdleTimeout = 7 * 24 * time.Hour // 7-day idle timeout

	m.Cookie.Name = CookieName
	m.Cookie.HttpOnly = true
	m.Cookie.Secure = !o.Insecure
	m.Cookie.SameSite = http.SameSiteLaxMode
	m.Cookie.Persist = true
	m.Cookie.Path = "/"

	return m
}
