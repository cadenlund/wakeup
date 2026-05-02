// Package middleware is the §4.7 cross-cutting HTTP middleware chain.
// One Go file per middleware; tests sit alongside as `<name>_test.go`.
//
// The chain is wired in `cmd/server/main.go` outside-in:
//
//  1. Recovery       — catches panics
//  2. RequestID      — reads/generates X-Request-ID
//  3. Logger         — slog line per request
//  4. CORS           — handled by go-chi/cors elsewhere
//  5. SecurityHeaders
//  6. RateLimit
//  7. SessionLoad    — `scs.LoadAndSave`
//  8. LoadUser       — populates ctx.User from the session
//  9. RequireAuth    — route-scoped
//  10. RequireAdmin  — route-scoped
//
// Every helper here returns an `http.Handler`-shaped middleware so callers
// can compose with chi.Router.Use(...) without an adapter.
package middleware

import (
	"context"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// ctxKey is unexported so external packages can't write to the same key
// by accident — they must call the typed accessors below.
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyUser
	ctxKeyRealUser
)

// WithRequestID returns ctx with the request id attached.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestIDFromContext returns the request id, or "" if unset.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// WithUser stores the effective user (impersonated if applicable, else
// the session owner) in ctx. Handlers should always read this for "who
// is acting" — never the raw session.
func WithUser(ctx context.Context, u *domain.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

// UserFromContext returns the effective user, or nil if the request is
// unauthenticated. Use RequireAuth to gate the handler instead of
// nil-checking everywhere.
func UserFromContext(ctx context.Context) *domain.User {
	u, _ := ctx.Value(ctxKeyUser).(*domain.User)
	return u
}

// WithRealUser stores the session-owner user (the admin during
// impersonation, otherwise == effective user). Audit logs read this.
func WithRealUser(ctx context.Context, u *domain.User) context.Context {
	return context.WithValue(ctx, ctxKeyRealUser, u)
}

// RealUserFromContext returns the session owner. Returns the effective
// user when no impersonation field has been set, so audit logs always
// have a concrete actor.
func RealUserFromContext(ctx context.Context) *domain.User {
	if u, ok := ctx.Value(ctxKeyRealUser).(*domain.User); ok && u != nil {
		return u
	}
	return UserFromContext(ctx)
}
