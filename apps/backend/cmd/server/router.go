package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	httpswagger "github.com/swaggo/http-swagger/v2"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/config"
	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
	"github.com/cadenlund/wakeup/apps/backend/internal/ratelimit"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	friendsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/friend"
	msgsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/message"
	notifprefsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	usersvc "github.com/cadenlund/wakeup/apps/backend/internal/service/user"

	// Importing the generated docs package as a blank registers the
	// OpenAPI spec with httpswagger so /v1/docs renders without a
	// separate spec file on disk. Path is kept inside the backend module
	// because Go can't import packages outside the module root.
	openapidocs "github.com/cadenlund/wakeup/apps/backend/internal/docs/openapi"
)

// routerDeps is everything cmd/server.main builds up before wiring routes.
// The struct exists so tests can construct a deterministic router without
// dragging in the full main() side effects (env, signal handlers).
type routerDeps struct {
	Cfg                 *config.Config
	Logger              *slog.Logger
	Pool                *pgxpool.Pool
	Redis               *redis.Client
	Sessions            *scs.SessionManager
	Limiter             *ratelimit.Limiter
	UserSvc             *usersvc.Service
	AuthSvc             *auth.Service
	NotifPrefSvc        *notifprefsvc.Service
	FriendSvc           *friendsvc.Service
	ConvSvc             *convsvc.Service
	MsgSvc              *msgsvc.Service
	UserHandler         *httpapi.UserHandler
	AuthHandler         *httpapi.AuthHandler
	FriendHandler       *httpapi.FriendHandler
	ConversationHandler *httpapi.ConversationHandler
	MessageHandler      *httpapi.MessageHandler

	// Rate-limit tier overrides. Zero values fall back to the
	// production §8.3 defaults (10/min auth, 60/min writes, 300/min
	// reads). Tests pass effectively-unlimited values to dodge
	// per-IP collisions across parallel runs on 127.0.0.1.
	RateLimitAuth   rateLimitTier
	RateLimitWrites rateLimitTier
	RateLimitReads  rateLimitTier
}

// rateLimitTier groups the §8.3 default scopes so we wire the chain
// once per group instead of duplicating the boilerplate.
type rateLimitTier struct {
	Scope  string
	Limit  int
	Window time.Duration
}

// Production §8.3 defaults. Test code overrides via routerDeps so
// parallel test runs (which all share 127.0.0.1 as the rate-limit
// identifier) don't collide on the per-IP auth-tier bucket.
var (
	rateLimitAuth   = rateLimitTier{Scope: "auth", Limit: 10, Window: time.Minute}
	rateLimitWrites = rateLimitTier{Scope: "writes", Limit: 60, Window: time.Minute}
	rateLimitReads  = rateLimitTier{Scope: "reads", Limit: 300, Window: time.Minute}
)

// resolveTier returns the override when set, otherwise the production
// default. An override is only honored when ALL three fields are
// populated — a partial override (e.g. Limit=10000, Window=time.Minute,
// Scope="") used to slip through and panic at request time inside
// mw.RateLimit (CodeRabbit caught this on PR #37).
func resolveTier(override, fallback rateLimitTier) rateLimitTier {
	if override.Scope != "" && override.Limit > 0 && override.Window > 0 {
		return override
	}
	return fallback
}

// buildRouter wires the §4.7 middleware chain and mounts every handler
// under /v1/*. All cross-cutting middleware lives in internal/middleware;
// this file only orchestrates.
func buildRouter(d routerDeps) (*chi.Mux, error) {
	if d.Cfg == nil || d.Logger == nil || d.Pool == nil || d.Redis == nil ||
		d.Sessions == nil || d.Limiter == nil ||
		d.UserSvc == nil || d.AuthSvc == nil || d.NotifPrefSvc == nil || d.FriendSvc == nil || d.ConvSvc == nil || d.MsgSvc == nil ||
		d.UserHandler == nil || d.AuthHandler == nil || d.FriendHandler == nil || d.ConversationHandler == nil || d.MessageHandler == nil {
		return nil, errors.New("buildRouter: all routerDeps fields are required")
	}

	authTier := resolveTier(d.RateLimitAuth, rateLimitAuth)
	writesTier := resolveTier(d.RateLimitWrites, rateLimitWrites)
	readsTier := resolveTier(d.RateLimitReads, rateLimitReads)

	r := chi.NewRouter()

	// §4.7 outer middleware chain.
	r.Use(mw.Recovery(d.Logger, httpapi.WriteError))
	r.Use(mw.RequestID)
	r.Use(mw.Logger(d.Logger))
	r.Use(corsMiddleware(d.Cfg))
	r.Use(mw.SecurityHeaders(d.Cfg.Env == "production"))

	// /v1/healthz, /v1/readyz, /v1/openapi.json, /v1/docs/* live OUTSIDE
	// auth/session/idempotency so the load balancer + browser can reach
	// them without state.
	r.Get("/v1/healthz", healthz)
	r.Get("/v1/readyz", readyz(d))
	r.Get("/v1/openapi.json", openAPISpec)
	r.Get("/v1/docs/*", httpswagger.Handler(
		httpswagger.URL("/v1/openapi.json"),
		httpswagger.DocExpansion("none"),
	))

	// Routes that need the session + per-route auth gating.
	r.Group(func(r chi.Router) {
		r.Use(d.Sessions.LoadAndSave)
		r.Use(mw.LoadUser(d.Sessions, d.UserSvc, httpapi.WriteError))

		// Public auth endpoints — register/login/password-reset run
		// without an authenticated session, but still pay the auth-tier
		// rate limit (10/min/IP) so brute force is bounded.
		r.Group(func(r chi.Router) {
			r.Use(mw.RateLimit(mw.RateLimitConfig{
				Limiter: d.Limiter,
				Scope:   authTier.Scope,
				Limit:   authTier.Limit,
				Window:  authTier.Window,
				Logger:  d.Logger,
			}, httpapi.WriteError))
			r.Post("/v1/auth/register", d.AuthHandler.Register)
			r.Post("/v1/auth/login", d.AuthHandler.Login)
			r.Post("/v1/auth/password-reset/request", d.AuthHandler.RequestPasswordReset)
			r.Post("/v1/auth/password-reset/confirm", d.AuthHandler.ConfirmPasswordReset)
			// Logout is idempotent (handler returns 204 even with no
			// active session), so it sits OUTSIDE RequireAuth so a
			// stale-cookie client can still drop their session cleanly.
			// CodeRabbit caught the previous misplacement on PR #28.
			r.Post("/v1/auth/logout", d.AuthHandler.Logout)
		})

		// Authenticated routes. Writes vs reads sit in separate scopes so
		// a user's reads can't starve their writes (different Redis keys).
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireAuth(httpapi.WriteError))

			r.Group(func(r chi.Router) {
				r.Use(mw.RateLimit(mw.RateLimitConfig{
					Limiter: d.Limiter, Scope: writesTier.Scope,
					Limit: writesTier.Limit, Window: writesTier.Window,
					Logger: d.Logger,
				}, httpapi.WriteError))
				r.Post("/v1/auth/logout-all", d.AuthHandler.LogoutAll)
				r.Patch("/v1/users/me", d.UserHandler.UpdateMe)
				r.Delete("/v1/users/me", d.UserHandler.DeleteMe)
				r.Post("/v1/users/me/avatar", d.UserHandler.UploadAvatar)
				r.Patch("/v1/users/me/notifications", d.UserHandler.UpdateNotifications)
				r.Post("/v1/friends/requests", d.FriendHandler.SendRequest)
				r.Post("/v1/friends/requests/{id}/accept", d.FriendHandler.AcceptRequest)
				r.Post("/v1/friends/requests/{id}/decline", d.FriendHandler.DeclineRequest)
				r.Delete("/v1/friends/{user_id}", d.FriendHandler.Unfriend)
				r.Post("/v1/friends/{user_id}/block", d.FriendHandler.Block)
				r.Delete("/v1/friends/{user_id}/block", d.FriendHandler.Unblock)
				r.Post("/v1/conversations", d.ConversationHandler.Create)
				r.Patch("/v1/conversations/{id}", d.ConversationHandler.Update)
				r.Delete("/v1/conversations/{id}", d.ConversationHandler.Leave)
				r.Post("/v1/conversations/{id}/members", d.ConversationHandler.AddMembers)
				r.Delete("/v1/conversations/{id}/members/{user_id}", d.ConversationHandler.RemoveMember)
				r.Post("/v1/conversations/{id}/read", d.ConversationHandler.MarkRead)
				r.Post("/v1/conversations/{id}/messages", d.MessageHandler.Send)
				r.Patch("/v1/messages/{id}", d.MessageHandler.Edit)
				r.Delete("/v1/messages/{id}", d.MessageHandler.Delete)
			})
			r.Group(func(r chi.Router) {
				r.Use(mw.RateLimit(mw.RateLimitConfig{
					Limiter: d.Limiter, Scope: readsTier.Scope,
					Limit: readsTier.Limit, Window: readsTier.Window,
					Logger: d.Logger,
				}, httpapi.WriteError))
				r.Get("/v1/auth/me", d.AuthHandler.Me)
				r.Get("/v1/users", d.UserHandler.Search)
				r.Get("/v1/users/{id}", d.UserHandler.GetByID)
				r.Get("/v1/users/me/notifications", d.UserHandler.GetNotifications)
				r.Get("/v1/friends", d.FriendHandler.List)
				r.Get("/v1/friends/requests", d.FriendHandler.ListRequests)
				r.Get("/v1/conversations", d.ConversationHandler.List)
				r.Get("/v1/conversations/{id}", d.ConversationHandler.Get)
				r.Get("/v1/conversations/{id}/messages", d.MessageHandler.List)
				r.Get("/v1/messages/{id}/reads", d.MessageHandler.ListReads)
			})
		})
	})

	return r, nil
}

// corsMiddleware builds the §8.4 CORS handler. Allowed origin is derived
// from cfg.CORSAllowedOrigins (comma-separated). AllowCredentials is
// always true — sessions need it.
func corsMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	origins := cfg.CORSOriginList()
	return cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           300,
	})
}

// healthz is the unauthenticated liveness probe — process is up.
//
// @Summary      Liveness probe
// @Description  Returns 200 unconditionally. The load balancer uses this to confirm the process is running; it does not check downstream dependencies (use readyz for that).
// @Tags         system
// @Produce      plain
// @Success      200  {string}  string  "ok"
// @Router       /v1/healthz [get]
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyz checks downstreams: Postgres + Redis. Each ping gets its own
// per-dependency timeout — a slow Postgres can't burn the whole budget
// and make Redis flap as a side effect (CodeRabbit caught the shared-
// context bug on PR #28).
//
// @Summary      Readiness probe
// @Description  Pings Postgres and Redis with independent 1.5s timeouts. Returns 200 only when both respond; otherwise 500 with a §4.4 envelope listing failed dependencies.
// @Tags         system
// @Produce      json
// @Success      200  {string}  string                "ok"
// @Failure      500  {object}  httpapi.ErrorResponse  "Dependency failure"
// @Router       /v1/readyz [get]
func readyz(d routerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			pgErr, redisErr error
		)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), readyDependencyTimeout)
			defer cancel()
			pgErr = d.Pool.Ping(ctx)
		}()
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), readyDependencyTimeout)
			defer cancel()
			redisErr = d.Redis.Ping(ctx).Err()
		}()
		wg.Wait()

		var failures []string
		if pgErr != nil {
			failures = append(failures, "postgres: "+pgErr.Error())
		}
		if redisErr != nil {
			failures = append(failures, "redis: "+redisErr.Error())
		}
		if len(failures) > 0 {
			httpapi.WriteError(w, r, apierror.Internal("not ready").
				WithCause(errors.New("readyz: "+joinStrings(failures))))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

// readyDependencyTimeout is the per-ping budget for /v1/readyz. Kept
// short so a wedged dependency can't stall the load balancer for long.
const readyDependencyTimeout = 1500 * time.Millisecond

// openAPISpec serves the generated swagger.json.
//
// docs.SwaggerInfo.ReadDoc() returns the spec as a string (the swag-
// generated package registers itself with the swag runtime); the
// blank import of openapidocs at the top of the file ensures the
// generated init() runs so the spec is registered.
//
// @Summary      OpenAPI spec
// @Description  Serves the generated OpenAPI 2.0 spec for this API. /v1/docs (Swagger UI) loads it from this endpoint.
// @Tags         system
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "OpenAPI document"
// @Router       /v1/openapi.json [get]
func openAPISpec(w http.ResponseWriter, _ *http.Request) {
	spec := openapidocs.SwaggerInfo.ReadDoc()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(spec))
}

// joinStrings concatenates parts with "; " separator. Inline because
// the standard library's strings.Join is the only call we'd make and
// router.go doesn't otherwise import strings.
func joinStrings(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}
