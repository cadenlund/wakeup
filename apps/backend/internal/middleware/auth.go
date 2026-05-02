package middleware

import (
	"context"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
)

// UserLoader is the §4.1 service-layer surface LoadUser needs to resolve
// a session's user_id into a *domain.User. The user service satisfies it
// directly (`*usersvc.Service.GetByID`); declaring the interface here
// keeps the middleware decoupled from the service package and respects
// the layering rule (handler → service → repository → storage).
//
// Implementations should return:
//   - apierror.NotFound (or any error matching apierror.CodeNotFound) when
//     the row is missing or soft-deleted — LoadUser will silently destroy
//     the session.
//   - any other error → propagated as Internal so writeError can render it.
type UserLoader interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// SessionUserIDKey is the scs session key the auth service writes the
// authenticated user_id under. Mirrors `auth.SessionUserIDKey` so the
// middleware can read the same value.
const SessionUserIDKey = "user_id"

// AuthRoles enumerates the §4.6 role values RequireRole consults.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// LoadUser is the §4.7 step 8 middleware. After scs.LoadAndSave has
// populated the session into ctx, this:
//   - reads `user_id` from the session
//   - delegates to a UserLoader (the user service in production) to fetch
//     the *domain.User
//   - attaches it to ctx via WithUser + WithRealUser
//
// Missing session, malformed user_id, or a row that's been soft-deleted
// out from under the session all silently leave ctx without a user.
// RequireAuth is the gate that produces a 401; LoadUser only enriches.
//
// writeError is required so any failure path emits the §4.4 envelope —
// no plaintext fallbacks (CodeRabbit caught those on PR #27).
//
// Impersonation (§8.7) is added in milestone 12.x by reading
// `impersonating_user_id` after the real user is loaded and overriding
// `WithUser` accordingly. The shape stays compatible.
func LoadUser(sm *scs.SessionManager, users UserLoader, writeError errorWriter) func(http.Handler) http.Handler {
	if writeError == nil {
		panic("middleware.LoadUser: nil writeError")
	}
	if sm == nil {
		panic("middleware.LoadUser: nil session manager")
	}
	if users == nil {
		panic("middleware.LoadUser: nil user loader")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := sm.GetString(r.Context(), SessionUserIDKey)
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			id, err := uuid.Parse(raw)
			if err != nil {
				// Garbled session — clear it via Destroy so a stale value
				// can't keep tripping this branch on every request.
				if destroyErr := sm.Destroy(r.Context()); destroyErr != nil {
					writeError(w, r, apierror.Internal("destroy session").WithCause(destroyErr))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			u, err := users.GetByID(r.Context(), id)
			if err != nil {
				if apierror.IsCode(err, apierror.CodeNotFound) {
					// Session points at a soft-deleted user — drop the
					// session so RequireAuth returns 401 cleanly.
					_ = sm.Destroy(r.Context())
					next.ServeHTTP(w, r)
					return
				}
				writeError(w, r, apierror.Internal("load session user").WithCause(err))
				return
			}
			loaded := u
			ctx := WithUser(r.Context(), &loaded)
			ctx = WithRealUser(ctx, &loaded)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth rejects requests that don't have a *domain.User on the
// context (set by LoadUser). writeError is required so the 401 always
// uses the §4.4 envelope shape.
func RequireAuth(writeError errorWriter) func(http.Handler) http.Handler {
	if writeError == nil {
		panic("middleware.RequireAuth: nil writeError")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u := UserFromContext(r.Context()); u != nil {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, r, apierror.Unauthorized("not authenticated"))
		})
	}
}

// RequireAdmin requires the request user (effective, not real) to have
// role=admin. Returns 401 when no user is loaded and 403 when the
// loaded user is non-admin.
func RequireAdmin(writeError errorWriter) func(http.Handler) http.Handler {
	if writeError == nil {
		panic("middleware.RequireAdmin: nil writeError")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if u == nil {
				writeError(w, r, apierror.Unauthorized("not authenticated"))
				return
			}
			if u.Role != RoleAdmin {
				writeError(w, r, apierror.Forbidden("admin role required"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
