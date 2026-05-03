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

// SessionImpersonatingKey is the scs session key the §8.7 admin
// impersonate handler writes when an admin starts acting as another
// user. LoadUser checks this AFTER loading the session owner; when set
// (and the target user is loadable), ctx.User is overridden with the
// target while ctx.RealUser stays as the admin.
const SessionImpersonatingKey = "impersonating_user_id"

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
					// session so RequireAuth returns 401 cleanly. If the
					// destroy itself fails the stale user_id stays put
					// and this branch keeps tripping on every request,
					// so we surface the failure the same way as the
					// malformed-ID path (CodeRabbit caught this on PR #27).
					if destroyErr := sm.Destroy(r.Context()); destroyErr != nil {
						writeError(w, r, apierror.Internal("destroy session").WithCause(destroyErr))
						return
					}
					next.ServeHTTP(w, r)
					return
				}
				writeError(w, r, apierror.Internal("load session user").WithCause(err))
				return
			}
			loaded := u
			ctx := WithRealUser(r.Context(), &loaded)
			effective := &loaded

			// §8.7 impersonation overlay. The admin handler stores the
			// target user_id under SessionImpersonatingKey via scs.Put;
			// resolve it here so the rest of the request sees ctx.User
			// as the impersonated user. Failures (parse error, target
			// soft-deleted) clear the field so the admin falls back to
			// acting as themselves rather than getting stuck.
			if rawTarget := sm.GetString(r.Context(), SessionImpersonatingKey); rawTarget != "" {
				targetID, parseErr := uuid.Parse(rawTarget)
				if parseErr != nil {
					sm.Remove(r.Context(), SessionImpersonatingKey)
				} else if target, lookupErr := users.GetByID(r.Context(), targetID); lookupErr != nil {
					if apierror.IsCode(lookupErr, apierror.CodeNotFound) {
						// Target was soft-deleted out from under the
						// session; drop the impersonation field but
						// keep the admin's own session intact.
						sm.Remove(r.Context(), SessionImpersonatingKey)
					} else {
						writeError(w, r, apierror.Internal("load impersonated user").WithCause(lookupErr))
						return
					}
				} else {
					t := target
					effective = &t
				}
			}
			ctx = WithUser(ctx, effective)
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
