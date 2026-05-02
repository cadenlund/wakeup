package middleware

import (
	"errors"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	repo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
)

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
//   - loads the *domain.User row (excluding soft-deleted)
//   - attaches it to ctx via WithUser + WithRealUser
//
// Missing session, malformed user_id, or a row that's been soft-deleted
// out from under the session all silently leave ctx without a user.
// RequireAuth is the gate that produces a 401; LoadUser only enriches.
//
// Impersonation (§8.7) is added in milestone 12.x by reading
// `impersonating_user_id` after the real user is loaded and overriding
// `WithUser` accordingly. The shape stays compatible.
func LoadUser(sm *scs.SessionManager, users *repo.Queries, writeError errorWriter) func(http.Handler) http.Handler {
	if sm == nil {
		panic("middleware.LoadUser: nil session manager")
	}
	if users == nil {
		panic("middleware.LoadUser: nil user repository")
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
				if destroyErr := sm.Destroy(r.Context()); destroyErr != nil && writeError != nil {
					writeError(w, r, apierror.Internal("destroy session").WithCause(destroyErr))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			u, err := users.GetByID(r.Context(), id)
			if err != nil {
				if errors.Is(err, repo.ErrNotFound) {
					// Session points at a soft-deleted user — drop the
					// session so RequireAuth returns 401 cleanly.
					_ = sm.Destroy(r.Context())
					next.ServeHTTP(w, r)
					return
				}
				if writeError != nil {
					writeError(w, r, apierror.Internal("load session user").WithCause(err))
					return
				}
				http.Error(w, "internal error", http.StatusInternalServerError)
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
// context (set by LoadUser). The §4.4 envelope is rendered via
// writeError; if it's nil, falls back to a plaintext 401.
func RequireAuth(writeError errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u := UserFromContext(r.Context()); u != nil {
				next.ServeHTTP(w, r)
				return
			}
			if writeError != nil {
				writeError(w, r, apierror.Unauthorized("not authenticated"))
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}

// RequireAdmin requires the request user (effective, not real) to have
// role=admin. Returns 401 when no user is loaded and 403 when the
// loaded user is non-admin.
func RequireAdmin(writeError errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if u == nil {
				if writeError != nil {
					writeError(w, r, apierror.Unauthorized("not authenticated"))
					return
				}
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if u.Role != RoleAdmin {
				if writeError != nil {
					writeError(w, r, apierror.Forbidden("admin role required"))
					return
				}
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// userOK is a small predicate used by tests to assert ctx.User shape
// without exporting the ctxKey type. (Not exported elsewhere — keep
// behind a build-tag-free internal helper if more callers grow.)
var _ = func(u *domain.User) bool { return u != nil && u.Role != "" }
