package testutil

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/config"
)

// Harness is the per-test fixture stack: pgtestdb-cloned database, real
// testcontainers redis, fakes for every external service, and an
// httptest.Server that hosts whatever router the caller wires in.
//
// PHASED BUILD: this struct is the §12.6 shape, but several fields are
// placeholders during Phase 1. As Phases 2 and 3 land their packages, the
// New constructor wires them in:
//
//   - Hub (ws.Hub)              → Phase 8.1
//   - Sentry init                → Phase 13.1
//   - Real router with services → Phase 3.9
//
// Until Phase 3.9 lands real handlers, the Server hosts an empty chi router
// so HTTPClient still works; AuthClient / AdminClient / WSDial panic with a
// clear "wire me in Phase X" message.
type Harness struct {
	Server  *httptest.Server
	Router  *chi.Mux
	DB      *pgxpool.Pool
	Redis   *redis.Client
	Mailer  *FakeMailer
	Pusher  *FakePusher
	Storage *FakeObjectStore
	Sentry  *SentryRecorder
	Cfg     config.Config

	// serverURL is parsed once for AuthClient's cookie jar.
	serverURL *url.URL
}

// New starts a fully-wired-as-of-Phase-1 harness. Each call gets:
//   - an isolated pgtestdb-cloned database (per-test)
//   - a shared testcontainers redis (sync.Once-cached) under a per-test
//     keyspace prefix derived from t.Name to avoid cross-test interference
//   - fresh fakes (Mailer / Pusher / Storage / Sentry)
//   - an empty chi router behind an httptest.Server
//
// t.Cleanup runs at end-of-test to close the redis client and the test
// server. The pool is closed by NewTestDB's own cleanup.
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

	router := chi.NewRouter()
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	srvURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Harness: parse server URL: %v", err)
	}

	return &Harness{
		Server:    server,
		Router:    router,
		DB:        pool,
		Redis:     redisClient,
		Mailer:    &FakeMailer{},
		Pusher:    &FakePusher{},
		Storage:   NewFakeObjectStore(),
		Sentry:    &SentryRecorder{},
		Cfg:       defaultTestConfig(),
		serverURL: srvURL,
	}
}

// HTTPClient returns an anonymous http.Client with a cookie jar pointed at
// the test server. Use it for routes that don't require auth.
func (h *Harness) HTTPClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("HTTPClient: jar: %v", err)
	}
	return &http.Client{Jar: jar}
}

// AuthClient registers + logs in as a fixture user, returns an authenticated
// HTTP client + the persisted user.
//
// NOT YET IMPLEMENTED — the auth service (Phase 3.2) and handlers (Phase 3.6)
// must land first. This method panics with a clear pointer to the milestone
// that fills it in.
func (h *Harness) AuthClient(t *testing.T) (*http.Client, any /* *domain.User after 3.1 */) {
	t.Helper()
	panic("Harness.AuthClient: wire me in Phase 3.6 (auth handler) — milestone 1.9 only ships scaffolding")
}

// AdminClient is AuthClient with role=admin pre-set. See AuthClient for the
// "not implemented" caveat.
func (h *Harness) AdminClient(t *testing.T) (*http.Client, any) {
	t.Helper()
	panic("Harness.AdminClient: wire me in Phase 12.5 (admin handler) — milestone 1.9 only ships scaffolding")
}

// WSDial dials /v1/ws authenticated as the given user. Lands in Phase 8.1
// when the WebSocket hub exists.
func (h *Harness) WSDial(t *testing.T, _ *http.Client) any {
	t.Helper()
	panic("Harness.WSDial: wire me in Phase 8.1 (websocket hub) — milestone 1.9 only ships scaffolding")
}

// defaultTestConfig builds a Config with the values a Phase-1 harness can
// honor. Phase 3.9 will replace this with config.Load reading the real
// .env.example so handler tests pick up CORS, session domain, etc.
func defaultTestConfig() config.Config {
	return config.Config{
		Env:              "local",
		LogLevel:         "info",
		HTTPAddr:         ":0",
		SessionDomain:    "localhost",
		S3ForcePathStyle: true,
	}
}
