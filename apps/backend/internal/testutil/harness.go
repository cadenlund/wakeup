package testutil

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // bucket name hash, not crypto
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/config"
	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
	wshandler "github.com/cadenlund/wakeup/apps/backend/internal/handler/ws"
	"github.com/cadenlund/wakeup/apps/backend/internal/objectstore"
	"github.com/cadenlund/wakeup/apps/backend/internal/pubsub"
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

// Harness is the per-test fixture stack: pgtestdb-cloned database, real
// testcontainers redis, fakes for every external service, and a TLS
// httptest.Server hosting whatever routes are wired in by Phase 3.x.
//
// PHASED BUILD: as later phases land more handlers/middleware, New
// extends the wiring. As of milestone 3.6 the harness wires:
//
//   - scs session manager + pgxstore (sessions table from migration 0002)
//   - auth service + AuthHandler mounted at /v1/auth/*
//   - notificationpref service (no handler yet — handler lands in 3.7)
//   - request-id helper middleware (proper middleware package lands in 3.8)
//
// Future milestones (Hub, real handlers, Sentry init) follow the same
// pattern: extend New, leave the panic-on-call helpers until ready.
type Harness struct {
	Server  *httptest.Server
	Router  *chi.Mux
	DB      *pgxpool.Pool
	Redis   *redis.Client
	Mailer  *FakeMailer
	Pusher  *FakePusher
	Storage *FakeObjectStore // in-memory store; not wired into services. Tests that exercise object storage hit the real MinIO via ObjStore below.
	Sentry  *SentryRecorder
	Cfg     config.Config

	// ObjStore is the production *objectstore.Store backed by the test
	// MinIO singleton. The user service uploads avatars through this.
	ObjStore   *objectstore.Store
	BucketName string

	// Services + repos exposed for tests that want to bypass HTTP. Tests
	// drive flows either via the wired router (the realistic path) or via
	// these direct handles when they need to fast-forward fixture state.
	Sessions     *scs.SessionManager
	UserRepo     *userrepo.Queries
	ResetsRepo   *passwordreset.Queries
	FriendRepo   *friendrepo.Queries
	ConvRepo     *convrepo.Queries
	MsgRepo      *msgrepo.Queries
	AttRepo      *attrepo.Queries
	NotifPrefSvc *notifprefsvc.Service
	AuthSvc      *auth.Service
	UserSvc      *usersvc.Service
	FriendSvc    *friendsvc.Service
	ConvSvc      *convsvc.Service
	MsgSvc       *msgsvc.Service
	AttSvc       *attsvc.Service
	Broker       pubsub.Broker

	// WS plumbing surfaced for handler-level tests that want to assert
	// pubsub fan-out into a connected client without re-constructing
	// the wiring.
	WSHub    *wshandler.Hub
	WSBridge *wshandler.Bridge

	serverURL *url.URL
}

// New starts a TLS test server with the Phase-3.6 service wiring. Each
// call gets:
//   - an isolated pgtestdb-cloned database
//   - a shared testcontainers redis under a per-test keyspace
//   - fresh fakes (Mailer / Pusher / Storage / Sentry)
//   - all wired-up services and a chi router mounting them behind
//     session-loading + request-id middleware
//
// Cookies use Secure=true (per §8.2's session config), which is why the
// test server is TLS — Go's cookiejar refuses to send Secure cookies
// over plain HTTP.
func New(t *testing.T) *Harness {
	t.Helper()

	pool := NewTestDB(t)

	redisURL := StartRedis(t)
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("Harness: parse redis URL: %v", err)
	}
	redisClient := redis.NewClient(redisOpts)
	t.Cleanup(func() { _ = redisClient.Close() })

	mailer := &FakeMailer{}
	users := userrepo.New(pool)
	resets := passwordreset.New(pool)
	notifPrefs := notifprefrepo.New(pool)
	friends := friendrepo.New(pool)
	convs := convrepo.New(pool)
	msgs := msgrepo.New(pool)
	atts := attrepo.New(pool)
	sm := session.New(pool)

	broker := pubsub.NewInProc(pubsub.NewRegistry())
	t.Cleanup(func() { _ = broker.Close() })

	endpoint := StartMinIO(t)
	bucket := perTestBucket(t)
	createBucket(t, endpoint, bucket)
	objStore, err := objectstore.New(objectstore.Config{
		Endpoint:       endpoint,
		Region:         "us-east-1",
		AccessKey:      MinIOAccessKey,
		SecretKey:      MinIOSecretKey,
		Bucket:         bucket,
		ForcePathStyle: true,
		// Sized to fit both avatars (5 MiB) and attachments (50 MiB) so
		// the same harness exercises both upload routes; +1 KiB slack
		// matches the handler-side multipart framing budget.
		MaxUploadBytes: attsvc.MaxAttachmentBytes + (1 << 10),
	})
	if err != nil {
		t.Fatalf("Harness: build objectstore: %v", err)
	}

	authSvc, err := auth.New(auth.Config{
		Pool: pool, Users: users, Resets: resets, Sessions: sm, Mailer: mailer,
	})
	if err != nil {
		t.Fatalf("Harness: build auth service: %v", err)
	}
	notifSvc, err := notifprefsvc.New(notifprefsvc.Config{Prefs: notifPrefs})
	if err != nil {
		t.Fatalf("Harness: build notificationpref service: %v", err)
	}
	userSvc, err := usersvc.New(usersvc.Config{Users: users, Storage: objStore})
	if err != nil {
		t.Fatalf("Harness: build user service: %v", err)
	}
	friendSvc, err := friendsvc.New(friendsvc.Config{Friends: friends, Users: users})
	if err != nil {
		t.Fatalf("Harness: build friend service: %v", err)
	}
	convSvc, err := convsvc.New(convsvc.Config{Pool: pool, Convs: convs, Users: users})
	if err != nil {
		t.Fatalf("Harness: build conversation service: %v", err)
	}
	msgSvc, err := msgsvc.New(msgsvc.Config{
		Pool: pool, Msgs: msgs, Convs: convs, Broker: broker,
	})
	if err != nil {
		t.Fatalf("Harness: build message service: %v", err)
	}
	attSvc, err := attsvc.New(attsvc.Config{Repo: atts, Storage: objStore})
	if err != nil {
		t.Fatalf("Harness: build attachment service: %v", err)
	}

	v := httpapi.NewValidator()
	authHandler, err := httpapi.NewAuthHandler(authSvc, v)
	if err != nil {
		t.Fatalf("Harness: build auth handler: %v", err)
	}
	userHandler, err := httpapi.NewUserHandler(userSvc, authSvc, notifSvc, v)
	if err != nil {
		t.Fatalf("Harness: build user handler: %v", err)
	}
	friendHandler, err := httpapi.NewFriendHandler(friendSvc, userSvc, authSvc, v)
	if err != nil {
		t.Fatalf("Harness: build friend handler: %v", err)
	}
	convHandler, err := httpapi.NewConversationHandler(convSvc, userSvc, authSvc, v)
	if err != nil {
		t.Fatalf("Harness: build conversation handler: %v", err)
	}
	msgHandler, err := httpapi.NewMessageHandler(msgSvc, authSvc, v)
	if err != nil {
		t.Fatalf("Harness: build message handler: %v", err)
	}
	attHandler, err := httpapi.NewAttachmentHandler(attSvc, authSvc)
	if err != nil {
		t.Fatalf("Harness: build attachment handler: %v", err)
	}

	// §8 WebSocket: build hub + bridge + upgrade handler so harness
	// users can dial /v1/ws like any other route. The bridge owns one
	// dispatcher goroutine for the lifetime of the harness.
	wsHub := wshandler.NewHub(nil)
	wsBridge, err := wshandler.NewBridge(wsHub, broker, nil)
	if err != nil {
		t.Fatalf("Harness: build ws bridge: %v", err)
	}
	t.Cleanup(wsBridge.Close)
	wsHandler, err := wshandler.NewHandler(wshandler.HandlerConfig{
		Hub: wsHub, Bridge: wsBridge, Broker: broker,
		Auth: authSvc, Convs: convSvc,
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("Harness: build ws handler: %v", err)
	}

	router := chi.NewRouter()
	router.Use(requestIDMiddleware) // §4.7 entry — full chain lands in 3.8.
	authHandler.Mount(router)
	userHandler.Mount(router)
	friendHandler.Mount(router)
	convHandler.Mount(router)
	msgHandler.Mount(router)
	attHandler.Mount(router)
	wsHandler.Mount(router)

	server := httptest.NewTLSServer(sm.LoadAndSave(router))
	t.Cleanup(server.Close)

	srvURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Harness: parse server URL: %v", err)
	}

	return &Harness{
		Server:       server,
		Router:       router,
		DB:           pool,
		Redis:        redisClient,
		Mailer:       mailer,
		Pusher:       &FakePusher{},
		Storage:      NewFakeObjectStore(),
		Sentry:       &SentryRecorder{},
		Cfg:          defaultTestConfig(),
		ObjStore:     objStore,
		BucketName:   bucket,
		Sessions:     sm,
		UserRepo:     users,
		ResetsRepo:   resets,
		FriendRepo:   friends,
		ConvRepo:     convs,
		MsgRepo:      msgs,
		AttRepo:      atts,
		NotifPrefSvc: notifSvc,
		AuthSvc:      authSvc,
		UserSvc:      userSvc,
		FriendSvc:    friendSvc,
		ConvSvc:      convSvc,
		MsgSvc:       msgSvc,
		AttSvc:       attSvc,
		Broker:       broker,
		WSHub:        wsHub,
		WSBridge:     wsBridge,
		serverURL:    srvURL,
	}
}

// perTestBucket builds a unique, S3-bucket-name-legal bucket id for the
// current test. SHA-1(t.Name())[:16] is short enough to fit in the 63-char
// cap with a "test-" prefix and contains only [0-9a-f].
func perTestBucket(t *testing.T) string {
	t.Helper()
	sum := sha1.Sum([]byte(t.Name())) //nolint:gosec
	return "test-" + hex.EncodeToString(sum[:])[:16]
}

// createBucket performs a one-shot CreateBucket against the test MinIO
// using a raw S3 client. objectstore.Store doesn't expose CreateBucket
// (that's deployment infra) — we drive it directly here. Idempotent:
// "BucketAlreadyOwnedByYou" is fine.
func createBucket(t *testing.T, endpoint, bucket string) {
	t.Helper()
	client := awss3.NewFromConfig(aws.Config{
		Region: "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(
			MinIOAccessKey, MinIOSecretKey, "",
		),
	}, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") {
		t.Fatalf("Harness: create bucket: %v", err)
	}
}

// HTTPClient returns an anonymous http.Client trusting the test server's
// self-signed TLS cert, with a fresh cookie jar attached. Use it for
// routes that don't require auth (or to drive register/login by hand).
func (h *Harness) HTTPClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("HTTPClient: jar: %v", err)
	}
	// Clone the server's transport so we trust its TLS cert without
	// turning off verification globally.
	srvClient := h.Server.Client()
	tr, ok := srvClient.Transport.(*http.Transport)
	if !ok || tr == nil {
		// Fall back to a permissive transport — only used in local tests.
		tr = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	} else {
		tr = tr.Clone()
	}
	return &http.Client{Transport: tr, Jar: jar}
}

// AuthClient registers a fresh user via the real /v1/auth/register
// endpoint, returns the cookie-jared client + the persisted domain.User.
// The user has a deterministic-random username/email; pass options to
// override.
func (h *Harness) AuthClient(t *testing.T, opts ...AuthClientOpt) (*http.Client, domain.User) {
	t.Helper()

	o := authClientOpts{
		password: "Password123!",
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.username == "" {
		// Username max is 32 chars, alphanumeric only (§4.6 + register
		// validator). 24-char hex prefix gives ~96 bits of entropy — more
		// than enough across parallel tests in one binary.
		o.username = "u" + uuidHex(t)[:24]
	}
	if o.email == "" {
		o.email = o.username + "@harness.test"
	}
	if o.displayName == "" {
		o.displayName = "Harness User"
	}

	client := h.HTTPClient(t)
	payload, err := json.Marshal(map[string]string{
		"username":     o.username,
		"email":        o.email,
		"display_name": o.displayName,
		"password":     o.password,
	})
	if err != nil {
		t.Fatalf("AuthClient: marshal register payload: %v", err)
	}
	resp, err := client.Post(h.Server.URL+"/v1/auth/register",
		"application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("AuthClient: register: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("AuthClient: register status = %d, want 201; body=%s", resp.StatusCode, string(respBody))
	}

	u, err := h.UserRepo.GetByUsername(t.Context(), o.username)
	if err != nil {
		t.Fatalf("AuthClient: load registered user: %v", err)
	}
	if o.role != "" && o.role != "user" {
		if err := h.UserRepo.UpdateRole(t.Context(), u.ID, o.role); err != nil {
			t.Fatalf("AuthClient: set role: %v", err)
		}
		u.Role = o.role
	}
	return client, u
}

// AdminClient is AuthClient with role=admin pre-set.
func (h *Harness) AdminClient(t *testing.T) (*http.Client, domain.User) {
	t.Helper()
	return h.AuthClient(t, WithRole("admin"))
}

// AuthClientOpt configures AuthClient's fixture user.
type AuthClientOpt func(*authClientOpts)

type authClientOpts struct {
	username    string
	email       string
	displayName string
	password    string
	role        string
}

// WithUsername overrides the random username default.
func WithUsername(s string) AuthClientOpt { return func(o *authClientOpts) { o.username = s } }

// WithEmail overrides the random email default.
func WithEmail(s string) AuthClientOpt { return func(o *authClientOpts) { o.email = s } }

// WithDisplayName overrides the default display name.
func WithDisplayName(s string) AuthClientOpt {
	return func(o *authClientOpts) { o.displayName = s }
}

// WithPassword overrides the default password (used to drive subsequent logins).
func WithPassword(s string) AuthClientOpt { return func(o *authClientOpts) { o.password = s } }

// WithRole sets a non-default role (e.g. "admin"). The fixture is upgraded
// after registration since the public Register endpoint always creates `user`.
func WithRole(s string) AuthClientOpt { return func(o *authClientOpts) { o.role = s } }

// WSDial dials /v1/ws authenticated as the given user. Lands in Phase 8.1
// when the WebSocket hub exists.
func (h *Harness) WSDial(t *testing.T, _ *http.Client) any {
	t.Helper()
	panic("Harness.WSDial: wire me in Phase 8.1 (websocket hub) — milestone 1.9 only ships scaffolding")
}

// requestIDMiddleware is the §4.7 minimal version: read X-Request-ID from
// the inbound request, generate one if missing, echo on the response.
// The full request-id middleware (with slog binding) lands in Phase 3.8.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			if v, err := uuid.NewV7(); err == nil {
				id = v.String()
			}
		}
		if id != "" {
			w.Header().Set("X-Request-ID", id)
		}
		next.ServeHTTP(w, r)
	})
}

// suffixCounter is a process-wide counter the handler test helpers use to
// produce unique alphanumeric usernames within the 32-char schema limit
// (UUIDs are 32 hex chars and overflow when prefixed; a small counter is
// plenty for one binary's worth of parallel subtests).
var suffixCounter atomic.Uint64

// NextSuffix returns a short alphanumeric suffix unique within this binary.
// Format: "<base36-counter>" — short enough to stay under the 32-char
// username cap when used with a prefix like "u".
func NextSuffix() string {
	n := suffixCounter.Add(1)
	return strconv.FormatUint(n, 36)
}

// uuidHex returns a fresh UUID v7 with dashes stripped — used to generate
// long, collision-resistant usernames for fixture users in a tight loop.
func uuidHex(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuidHex: %v", err)
	}
	hex := make([]byte, 0, 32)
	for _, b := range id[:] {
		const digits = "0123456789abcdef"
		hex = append(hex, digits[b>>4], digits[b&0xF])
	}
	return string(hex)
}

// defaultTestConfig builds a Config with the values a Phase-3.6 harness
// needs. Phase 3.9 will replace this with config.Load reading the real
// .env.example so handler tests also pick up CORS, session domain, etc.
func defaultTestConfig() config.Config {
	return config.Config{
		Env:              "local",
		LogLevel:         "info",
		HTTPAddr:         ":0",
		SessionDomain:    "localhost",
		S3ForcePathStyle: true,
	}
}
