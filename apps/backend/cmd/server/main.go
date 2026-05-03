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
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	"github.com/cadenlund/wakeup/apps/backend/internal/ratelimit"
	attrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/attachment"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	friendrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	msgrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	notifprefrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/passwordreset"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	attsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/attachment"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	friendsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/friend"
	msgsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/message"
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
	friendsRepo := friendrepo.New(pool)
	convsRepo := convrepo.New(pool)
	msgsRepo := msgrepo.New(pool)
	attsRepo := attrepo.New(pool)

	// Pubsub broker (§4.5). Production wires Redis pubsub so events fan
	// out across replicas; the broker's Close runs on the way down.
	broker := pubsub.NewRedis(redisClient)
	defer func() { _ = broker.Close() }()

	// §8.2 locks Cookie.Secure=true in production. Local/test envs run
	// over plain HTTP (`just dev`), so the browser would refuse to send
	// the session cookie back. Relax Secure outside production —
	// CodeRabbit caught the mismatch on PR #28.
	sessions := session.New(pool, session.Options{
		Insecure: cfg.Env != "production",
	})
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
	friendSvc, err := friendsvc.New(friendsvc.Config{Friends: friendsRepo, Users: users})
	if err != nil {
		return fmt.Errorf("friend service: %w", err)
	}
	convSvc, err := convsvc.New(convsvc.Config{Pool: pool, Convs: convsRepo, Users: users})
	if err != nil {
		return fmt.Errorf("conversation service: %w", err)
	}
	messageSvc, err := msgsvc.New(msgsvc.Config{
		Pool: pool, Msgs: msgsRepo, Convs: convsRepo, Broker: broker, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("message service: %w", err)
	}
	attachmentSvc, err := attsvc.New(attsvc.Config{
		Repo: attsRepo, Storage: objStore, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("attachment service: %w", err)
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
	friendHandler, err := httpapi.NewFriendHandler(friendSvc, userSvc, authSvc, v)
	if err != nil {
		return fmt.Errorf("friend handler: %w", err)
	}
	convHandler, err := httpapi.NewConversationHandler(convSvc, userSvc, authSvc, v)
	if err != nil {
		return fmt.Errorf("conversation handler: %w", err)
	}
	messageHandler, err := httpapi.NewMessageHandler(messageSvc, authSvc, v)
	if err != nil {
		return fmt.Errorf("message handler: %w", err)
	}
	attachmentHandler, err := httpapi.NewAttachmentHandler(attachmentSvc, authSvc)
	if err != nil {
		return fmt.Errorf("attachment handler: %w", err)
	}

	router, err := buildRouter(routerDeps{
		Cfg:                 cfg,
		Logger:              logger,
		Pool:                pool,
		Redis:               redisClient,
		Sessions:            sessions,
		Limiter:             limiter,
		UserSvc:             userSvc,
		AuthSvc:             authSvc,
		NotifPrefSvc:        notifPrefSvc,
		FriendSvc:           friendSvc,
		ConvSvc:             convSvc,
		MsgSvc:              messageSvc,
		AttSvc:              attachmentSvc,
		UserHandler:         userHandler,
		AuthHandler:         authHandler,
		FriendHandler:       friendHandler,
		ConversationHandler: convHandler,
		MessageHandler:      messageHandler,
		AttachmentHandler:   attachmentHandler,
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
// ResendAPIKey is empty in local/test envs, it returns a no-op so
// `just dev` works without a Resend key. In any other env (staging,
// production) a missing key is a startup error — silently no-op'ing
// would turn a bad secret rollout into a silent password-reset outage
// (CodeRabbit caught this on PR #28).
func buildMailer(cfg *config.Config) (mailer.Mailer, error) {
	if cfg.ResendAPIKey == "" {
		switch cfg.Env {
		case "local", "test":
			return noopMailer{}, nil
		default:
			return nil, fmt.Errorf("mailer: RESEND_API_KEY is required in env=%s", cfg.Env)
		}
	}
	return mailer.New(mailer.Config{
		APIKey:       cfg.ResendAPIKey,
		FromEmail:    cfg.ResendFromEmail,
		ResetURLBase: "https://wakeup.app/auth/reset?token=",
	})
}

type noopMailer struct{}

func (noopMailer) SendPasswordReset(_ context.Context, _, _ string) error { return nil }
