// @title           Wakeup API
// @version         1.0
// @description     Friend-graph chat backend. See docs/WAKEUP.md for the full spec.
// @host            localhost:8080
// @BasePath        /
// @schemes         http https
//
// @securityDefinitions.apikey CookieAuth
// @in cookie
// @name wakeup_session
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/config"
	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
	"github.com/cadenlund/wakeup/apps/backend/internal/log"
	"github.com/cadenlund/wakeup/apps/backend/internal/mailer"
	"github.com/cadenlund/wakeup/apps/backend/internal/objectstore"
	"github.com/cadenlund/wakeup/apps/backend/internal/ratelimit"
	notifprefrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/passwordreset"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	notifprefsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	usersvc "github.com/cadenlund/wakeup/apps/backend/internal/service/user"
	"github.com/cadenlund/wakeup/apps/backend/internal/session"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run owns the process lifecycle: load config, build deps, serve until
// SIGINT/SIGTERM, drain. Splitting from main keeps the os.Exit out of
// the unit-testable path.
func run() error {
	cfg, err := config.Load(config.LoadOpts{EnvFilePath: ".env"})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger := log.New(cfg.LogLevel)
	logger.Info("wakeup starting",
		slog.String("env", cfg.Env),
		slog.String("addr", cfg.HTTPAddr),
	)

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	pool, err := pgxpool.New(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(rootCtx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis url: %w", err)
	}
	redisClient := redis.NewClient(redisOpts)
	defer func() { _ = redisClient.Close() }()
	if err := redisClient.Ping(rootCtx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}

	objStore, err := objectstore.New(objectstore.Config{
		Endpoint:       cfg.S3Endpoint,
		Region:         cfg.S3Region,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		Bucket:         cfg.S3Bucket,
		ForcePathStyle: cfg.S3ForcePathStyle,
		MaxUploadBytes: 50 << 20, // 50 MiB §4.6 attachment cap; avatars share the store.
	})
	if err != nil {
		return fmt.Errorf("objectstore: %w", err)
	}

	mailerImpl, err := buildMailer(cfg)
	if err != nil {
		return fmt.Errorf("mailer: %w", err)
	}

	users := userrepo.New(pool)
	resets := passwordreset.New(pool)
	prefsRepo := notifprefrepo.New(pool)

	sessions := session.New(pool)
	limiter := ratelimit.New(redisClient)

	authSvc, err := auth.New(auth.Config{
		Pool: pool, Users: users, Resets: resets, Sessions: sessions, Mailer: mailerImpl,
	})
	if err != nil {
		return fmt.Errorf("auth service: %w", err)
	}
	userSvc, err := usersvc.New(usersvc.Config{Users: users, Storage: objStore})
	if err != nil {
		return fmt.Errorf("user service: %w", err)
	}
	notifPrefSvc, err := notifprefsvc.New(notifprefsvc.Config{Prefs: prefsRepo})
	if err != nil {
		return fmt.Errorf("notificationpref service: %w", err)
	}

	v := httpapi.NewValidator()
	authHandler, err := httpapi.NewAuthHandler(authSvc, v)
	if err != nil {
		return fmt.Errorf("auth handler: %w", err)
	}
	userHandler, err := httpapi.NewUserHandler(userSvc, authSvc, notifPrefSvc, v)
	if err != nil {
		return fmt.Errorf("user handler: %w", err)
	}

	router, err := buildRouter(routerDeps{
		Cfg:          cfg,
		Logger:       logger,
		Pool:         pool,
		Redis:        redisClient,
		Sessions:     sessions,
		Limiter:      limiter,
		UserSvc:      userSvc,
		AuthSvc:      authSvc,
		NotifPrefSvc: notifPrefSvc,
		UserHandler:  userHandler,
		AuthHandler:  authHandler,
	})
	if err != nil {
		return fmt.Errorf("router: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Listen on a goroutine so we can run the signal handler in the
	// foreground. Errors flow through serveErr so the main goroutine
	// can decide what to print.
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", slog.String("addr", server.Addr))
		serveErr <- server.ListenAndServe()
	}()

	// §4.9 graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("shutdown signal received", slog.String("signal", sig.String()))
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown", slog.String("error", err.Error()))
	}
	logger.Info("wakeup exiting cleanly")
	return nil
}

// buildMailer returns a Resend-backed mailer in production. When
// ResendAPIKey is empty (the local default), it returns a no-op
// implementation so `just dev` doesn't require a Resend key just to
// stand the server up. The password-reset flow simply has no observable
// side effect in that case.
func buildMailer(cfg *config.Config) (mailer.Mailer, error) {
	if cfg.ResendAPIKey == "" {
		return noopMailer{}, nil
	}
	return mailer.New(mailer.Config{
		APIKey:       cfg.ResendAPIKey,
		FromEmail:    cfg.ResendFromEmail,
		ResetURLBase: "https://wakeup.app/auth/reset?token=",
	})
}

type noopMailer struct{}

func (noopMailer) SendPasswordReset(_ context.Context, _, _ string) error { return nil }
