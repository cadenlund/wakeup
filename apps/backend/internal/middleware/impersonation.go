package middleware

import (
	"net/http"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
)

// BlockDuringImpersonation is the §8.7 / §12.4 guard that rejects
// requests when the caller is impersonating another user
// (`ctx.User != ctx.RealUser`). Routes wired with this middleware
// return 403 `BLOCKED_DURING_IMPERSONATION` for any in-progress
// impersonation session — the spec lists:
//
//   - POST  /v1/auth/password-reset/*    (would lock the user out)
//   - POST  /v1/auth/logout-all          (would orphan the admin's other sessions)
//   - DELETE /v1/users/me                (destructive)
//   - PATCH  /v1/users/me/notifications  (surprising side effect for the user)
//
// Stack ordering: this must come AFTER LoadUser so both ctx.User and
// ctx.RealUser are populated, and BEFORE any handler that would mutate
// state. RequireAuth also belongs upstream — an unauthenticated request
// has no impersonation to check, but RequireAuth's 401 should fire
// first so the unauth case keeps its existing wire shape.
func BlockDuringImpersonation(writeError errorWriter) func(http.Handler) http.Handler {
	if writeError == nil {
		panic("middleware.BlockDuringImpersonation: nil writeError")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			eff := UserFromContext(r.Context())
			session := RealUserFromContext(r.Context())
			if eff != nil && session != nil && eff.ID != session.ID {
				writeError(w, r, apierror.BlockedDuringImpersonation(""))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
