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
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	lkauth "github.com/livekit/protocol/auth"
	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/config"
	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
	wshandler "github.com/cadenlund/wakeup/apps/backend/internal/handler/ws"
	"github.com/cadenlund/wakeup/apps/backend/internal/job"
	"github.com/cadenlund/wakeup/apps/backend/internal/log"
	"github.com/cadenlund/wakeup/apps/backend/internal/mailer"
	mw "github.com/cadenlund/wakeup/apps/backend/internal/middleware"
	"github.com/cadenlund/wakeup/apps/backend/internal/objectstore"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
	"github.com/cadenlund/wakeup/apps/backend/internal/pushnotif"
	"github.com/cadenlund/wakeup/apps/backend/internal/ratelimit"
	attrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/attachment"
	auditrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/audit"
	convrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/conversation"
	devicerepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/devicetoken"
	friendrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/friendship"
	idemrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/idempotency"
	msgrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/message"
	notifrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/notification"
	notifprefrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/notificationpref"
	"github.com/cadenlund/wakeup/apps/backend/internal/repository/passwordreset"
	presrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/presence"
	userrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/user"
	sentryclient "github.com/cadenlund/wakeup/apps/backend/internal/sentry"
	adminsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/admin"
	attsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/attachment"
	"github.com/cadenlund/wakeup/apps/backend/internal/service/auth"
	contactssvc "github.com/cadenlund/wakeup/apps/backend/internal/service/contacts"
	convsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/conversation"
	devicesvc "github.com/cadenlund/wakeup/apps/backend/internal/service/device"
	friendsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/friend"
	msgsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/message"
	notifsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/notification"
	notifprefsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/notificationpref"
	presencesvc "github.com/cadenlund/wakeup/apps/backend/internal/service/presence"
	roomsvc "github.com/cadenlund/wakeup/apps/backend/internal/service/room"
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
	pusher, err := buildPusher(cfg)
	if err != nil {
		return fmt.Errorf("pushnotif: %w", err)
	}
	sentryClient, err := buildSentry(cfg)
	if err != nil {
		return fmt.Errorf("sentry: %w", err)
	}
	if sentryClient != nil {
		// SIGTERM path drains the queue before exit so in-flight
		// captures don't get dropped on graceful shutdown. A timeout
		// here (2s isn't enough to flush the queue) is rare but worth
		// surfacing — silently dropping events on shutdown would mean
		// the worst-failure cluster of errors never reaches Sentry.
		defer func() {
			if ok := sentryClient.Flush(2 * time.Second); !ok {
				logger.Warn("sentry flush timed out before exit; some events may be dropped")
			}
		}()
	}

	users := userrepo.New(pool)
	resets := passwordreset.New(pool)
	prefsRepo := notifprefrepo.New(pool)
	friendsRepo := friendrepo.New(pool)
	convsRepo := convrepo.New(pool)
	msgsRepo := msgrepo.New(pool)
	attsRepo := attrepo.New(pool)
	presRepo := presrepo.New(pool)
	devicesRepo := devicerepo.New(pool)
	auditsRepo := auditrepo.New(pool)
	idemKeysRepo := idemrepo.New(pool)

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
	pushSuppression := notifrepo.New(pool)
	notificationSvc, err := notifsvc.New(notifsvc.Config{
		Prefs: notifPrefSvc, Devices: devicesRepo, Pusher: pusher,
		Suppression: pushSuppression,
	})
	if err != nil {
		return fmt.Errorf("notification service: %w", err)
	}
	convSvc, err := convsvc.New(convsvc.Config{Pool: pool, Convs: convsRepo, Users: users})
	if err != nil {
		return fmt.Errorf("conversation service: %w", err)
	}
	// presenceSvc is built before friend/message so it can be wired into
	// both as the §11.5 PresenceLister. presenceSvc itself takes a
	// FriendListGetter — we resolve that with a thin lazy adapter so we
	// don't need a two-pass construction for the cycle.
	friendSvcRef := &lazyFriendList{}
	presenceSvc, err := presencesvc.New(presencesvc.Config{
		Repo: presRepo, Broker: broker, Friends: friendSvcRef, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("presence service: %w", err)
	}
	friendSvc, err := friendsvc.New(friendsvc.Config{
		Friends: friendsRepo, Users: users,
		Presence: presenceSvc, Notifications: notificationSvc, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("friend service: %w", err)
	}
	friendSvcRef.inner = friendSvc
	messageSvc, err := msgsvc.New(msgsvc.Config{
		Pool: pool, Msgs: msgsRepo, Convs: convsRepo, Broker: broker, Logger: logger,
		Presence: presenceSvc, Notifications: notificationSvc,
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
	livekitAdmin, err := roomsvc.NewLiveKitAdmin(cfg.LiveKitURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)
	if err != nil {
		return fmt.Errorf("livekit admin client: %w", err)
	}
	loneKickAfter, err := cfg.RoomLoneKickAfterDuration()
	if err != nil {
		return err
	}
	roomSvc, err := roomsvc.New(roomsvc.Config{
		Convs: convSvc, Users: users,
		APIKey: cfg.LiveKitAPIKey, APISecret: cfg.LiveKitAPISecret,
		LiveKitURL:    cfg.LiveKitURL,
		Redis:         redisClient,
		Logger:        logger,
		LiveKitAdmin:  livekitAdmin,
		LoneKickAfter: loneKickAfter,
	})
	if err != nil {
		return fmt.Errorf("room service: %w", err)
	}
	deviceSvc, err := devicesvc.New(devicesvc.Config{Devices: devicesRepo})
	if err != nil {
		return fmt.Errorf("device service: %w", err)
	}
	adminSvc, err := adminsvc.New(adminsvc.Config{Pool: pool, Users: users, Audit: auditsRepo})
	if err != nil {
		return fmt.Errorf("admin service: %w", err)
	}

	// §4.12 background-job runner. Phase 7.4 registers the attachment
	// orphan sweeper; later phases add presence / idempotency / session
	// sweepers to the same runner.
	jobRunner := job.New(logger)
	orphanSweeper, err := attsvc.NewOrphanSweeper(attsvc.OrphanSweeperConfig{
		Repo: attsRepo, Storage: objStore, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("attachment orphan sweeper: %w", err)
	}
	jobRunner.Register(orphanSweeper)
	// §9.2 presence decay sweeper: online → away → offline as
	// last_active_at ages out. Registered against the same runner so
	// graceful shutdown stops both jobs together.
	jobRunner.Register(presenceSvc)
	// §4.8 idempotency sweeper: drops expired idempotency_keys rows
	// every hour so the cache table doesn't grow unbounded.
	idempotencySweeper, err := mw.NewIdempotencySweeper(mw.IdempotencySweeperConfig{
		Repo: idemKeysRepo, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("idempotency sweeper: %w", err)
	}
	jobRunner.Register(idempotencySweeper)
	// §10.3 lone-user kick sweeper: drops participants who've been alone
	// in a room past their deadline (Discord-style). Wired here so it
	// shares graceful-shutdown with the rest of the runner.
	loneKickSweeper, err := roomsvc.NewLoneKickSweeper(roomSvc, logger, 0)
	if err != nil {
		return fmt.Errorf("lone-kick sweeper: %w", err)
	}
	jobRunner.Register(loneKickSweeper)
	jobRunner.Start(rootCtx)
	defer jobRunner.Stop()

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
	presenceHandler, err := httpapi.NewPresenceHandler(presenceSvc, userSvc, authSvc, v)
	if err != nil {
		return fmt.Errorf("presence handler: %w", err)
	}
	roomHandler, err := httpapi.NewRoomHandler(roomSvc, authSvc, v)
	if err != nil {
		return fmt.Errorf("room handler: %w", err)
	}
	deviceHandler, err := httpapi.NewDeviceHandler(deviceSvc, authSvc, v)
	if err != nil {
		return fmt.Errorf("device handler: %w", err)
	}
	adminHandler, err := httpapi.NewAdminHandler(adminSvc, authSvc, sessions, v)
	if err != nil {
		return fmt.Errorf("admin handler: %w", err)
	}
	contactsSvc, err := contactssvc.New(contactssvc.Config{Users: users})
	if err != nil {
		return fmt.Errorf("contacts service: %w", err)
	}
	contactsHandler, err := httpapi.NewContactsHandler(contactsSvc, authSvc, v)
	if err != nil {
		return fmt.Errorf("contacts handler: %w", err)
	}
	livekitWebhookHandler, err := httpapi.NewLiveKitWebhookHandler(
		roomSvc, broker,
		lkauth.NewSimpleKeyProvider(cfg.LiveKitAPIKey, cfg.LiveKitAPISecret),
		logger,
		httpapi.LiveKitWebhookHandlerConfig{
			Convs:         convsRepo,
			Presence:      presenceSvc,
			Notifications: notificationSvc,
		},
	)
	if err != nil {
		return fmt.Errorf("livekit webhook handler: %w", err)
	}

	// §8 WebSocket realtime: hub + bridge + upgrade handler. The bridge
	// drains the broker (Redis pubsub in prod) and fans events out to
	// connected users on this instance. defer Close so a SIGTERM
	// triggers a clean dispatcher shutdown.
	wsHub := wshandler.NewHub(logger)
	wsBridge, err := wshandler.NewBridge(wsHub, broker, logger)
	if err != nil {
		return fmt.Errorf("ws bridge: %w", err)
	}
	defer wsBridge.Close()
	wsHandler, err := wshandler.NewHandler(wshandler.HandlerConfig{
		Hub: wsHub, Bridge: wsBridge, Broker: broker,
		Auth: authSvc, Convs: convSvc, Logger: logger,
		AllowedOrigins: cfg.CORSOriginList(),
		WriteError:     httpapi.WriteError,
	})
	if err != nil {
		return fmt.Errorf("ws handler: %w", err)
	}

	deps := routerDeps{
		Cfg:                   cfg,
		Logger:                logger,
		Pool:                  pool,
		Redis:                 redisClient,
		Sessions:              sessions,
		Limiter:               limiter,
		IdempotencyRepo:       idemKeysRepo,
		UserSvc:               userSvc,
		AuthSvc:               authSvc,
		NotifPrefSvc:          notifPrefSvc,
		FriendSvc:             friendSvc,
		ConvSvc:               convSvc,
		MsgSvc:                messageSvc,
		AttSvc:                attachmentSvc,
		PresenceSvc:           presenceSvc,
		RoomSvc:               roomSvc,
		DeviceSvc:             deviceSvc,
		AdminSvc:              adminSvc,
		UserHandler:           userHandler,
		AuthHandler:           authHandler,
		FriendHandler:         friendHandler,
		ConversationHandler:   convHandler,
		MessageHandler:        messageHandler,
		AttachmentHandler:     attachmentHandler,
		PresenceHandler:       presenceHandler,
		RoomHandler:           roomHandler,
		DeviceHandler:         deviceHandler,
		AdminHandler:          adminHandler,
		ContactsHandler:       contactsHandler,
		LiveKitWebhookHandler: livekitWebhookHandler,
		WSHandler:             wsHandler,
	}
	// Avoid the typed-nil-as-interface gotcha: only assign Sentry when
	// we actually have a concrete client. A nil `*sentryclient.Client`
	// stored into an interface field would compare != nil downstream.
	if sentryClient != nil {
		deps.Sentry = sentryClient
	}
	router, err := buildRouter(deps)
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

// buildPusher returns an Expo-backed pusher in production. Mirrors
// buildMailer's noop-in-dev pattern so `just dev` doesn't need an
// EXPO_ACCESS_TOKEN. Outside local/test, a missing token is fatal —
// silently no-op'ing would mean every offline-push trigger drops on
// the floor without anyone noticing.
func buildPusher(cfg *config.Config) (pushnotif.Pusher, error) {
	if cfg.ExpoAccessToken == "" {
		switch cfg.Env {
		case "local", "test":
			return noopPusher{}, nil
		default:
			return nil, fmt.Errorf("pushnotif: EXPO_ACCESS_TOKEN is required in env=%s", cfg.Env)
		}
	}
	return pushnotif.New(pushnotif.Config{AccessToken: cfg.ExpoAccessToken})
}

type noopPusher struct{}

func (noopPusher) Send(_ context.Context, _ []string, _ pushnotif.Notification) error {
	return nil
}

// buildSentry returns a §13.1 Sentry client when a DSN is configured.
// Mirrors the buildMailer / buildPusher pattern: blank DSN in
// local/test envs is allowed (returns nil so the recovery middleware
// skips capture); blank DSN in any other env is fatal so a missing
// secret rollout doesn't silently kill error visibility.
//
// Whitespace-only DSNs are treated as blank — copy-pasting a DSN with
// trailing whitespace would otherwise sail past the empty check and
// then fail at SDK Init with a less obvious error.
func buildSentry(cfg *config.Config) (*sentryclient.Client, error) {
	dsn := strings.TrimSpace(cfg.SentryDSN)
	if dsn == "" {
		switch cfg.Env {
		case "local", "test":
			return nil, nil
		default:
			return nil, fmt.Errorf("sentry: SENTRY_DSN is required in env=%s", cfg.Env)
		}
	}
	return sentryclient.New(sentryclient.Config{
		DSN:         dsn,
		Environment: cfg.SentryEnvironment,
	})
}

// lazyFriendList is the §11.5 wire-up adapter that breaks the
// friend↔presence construction cycle. presence.Service needs a
// FriendLister at construction time so it can fan out presence updates
// to friends; friend.Service (post-11.5) needs a PresenceLister so it
// can gate offline pushes. This adapter holds a *friendsvc.Service that
// the caller assigns once both are built — every method delegates.
type lazyFriendList struct {
	inner *friendsvc.Service
}

func (l *lazyFriendList) ListAcceptedFriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	if l.inner == nil {
		return nil, nil
	}
	return l.inner.ListAcceptedFriendIDs(ctx, userID)
}
