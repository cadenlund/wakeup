package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/config"
	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
	"github.com/cadenlund/wakeup/apps/backend/internal/ratelimit"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// productionLikeServer wires the buildRouter() output (the same one main()
// uses) over the harness's services and a TLS test server so we exercise
// the full chain — including session cookies, request-id, recovery,
// CORS, rate limit, and the §4.7 auth gating.
func productionLikeServer(t *testing.T) (*httptest.Server, *http.Client, *testutil.Harness) {
	t.Helper()
	h := testutil.New(t)

	cfg := &config.Config{
		Env:                "local",
		LogLevel:           "info",
		HTTPAddr:           ":0",
		SessionDomain:      "localhost",
		CORSAllowedOrigins: "https://wakeup.app",
	}

	v := httpapi.NewValidator()
	authHandler, err := httpapi.NewAuthHandler(h.AuthSvc, v)
	if err != nil {
		t.Fatalf("auth handler: %v", err)
	}
	userHandler, err := httpapi.NewUserHandler(h.UserSvc, h.AuthSvc, h.NotifPrefSvc, v)
	if err != nil {
		t.Fatalf("user handler: %v", err)
	}
	friendHandler, err := httpapi.NewFriendHandler(h.FriendSvc, h.UserSvc, h.AuthSvc, v)
	if err != nil {
		t.Fatalf("friend handler: %v", err)
	}
	convHandler, err := httpapi.NewConversationHandler(h.ConvSvc, h.UserSvc, h.AuthSvc, v)
	if err != nil {
		t.Fatalf("conversation handler: %v", err)
	}

	// Each test gets a unique rate-limit scope so parallel smoke tests
	// (all bound to 127.0.0.1, all sharing the testcontainer redis)
	// don't collide on the auth-tier per-IP bucket. Limits stay
	// production-shaped — only the keyspace differs.
	suffix := "-" + testutil.NextSuffix()
	router, err := buildRouter(routerDeps{
		Cfg:                 cfg,
		Logger:              slog.Default(),
		Pool:                h.DB,
		Redis:               h.Redis,
		Sessions:            h.Sessions,
		Limiter:             ratelimit.New(h.Redis),
		UserSvc:             h.UserSvc,
		AuthSvc:             h.AuthSvc,
		NotifPrefSvc:        h.NotifPrefSvc,
		FriendSvc:           h.FriendSvc,
		ConvSvc:             h.ConvSvc,
		UserHandler:         userHandler,
		AuthHandler:         authHandler,
		FriendHandler:       friendHandler,
		ConversationHandler: convHandler,
		RateLimitAuth:       rateLimitTier{Scope: "auth" + suffix, Limit: 10000, Window: time.Minute},
		RateLimitWrites:     rateLimitTier{Scope: "writes" + suffix, Limit: 10000, Window: time.Minute},
		RateLimitReads:      rateLimitTier{Scope: "reads" + suffix, Limit: 10000, Window: time.Minute},
	})
	if err != nil {
		t.Fatalf("buildRouter: %v", err)
	}

	server := httptest.NewTLSServer(router)
	t.Cleanup(server.Close)

	jar, _ := cookiejar.New(nil)
	tr, _ := server.Client().Transport.(*http.Transport)
	if tr == nil {
		tr = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	} else {
		tr = tr.Clone()
	}
	c := &http.Client{Transport: tr, Jar: jar}
	return server, c, h
}

// --- Smoke checks -------------------------------------------------------

func TestRouter_Healthz(t *testing.T) {
	t.Parallel()
	srv, c, _ := productionLikeServer(t)
	resp, err := c.Get(srv.URL + "/v1/healthz")
	if err != nil {
		t.Fatalf("GET /v1/healthz: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestRouter_Readyz(t *testing.T) {
	t.Parallel()
	srv, c, _ := productionLikeServer(t)
	resp, err := c.Get(srv.URL + "/v1/readyz")
	if err != nil {
		t.Fatalf("GET /v1/readyz: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestRouter_OpenAPISpec(t *testing.T) {
	t.Parallel()
	srv, c, _ := productionLikeServer(t)
	resp, err := c.Get(srv.URL + "/v1/openapi.json")
	if err != nil {
		t.Fatalf("GET /v1/openapi.json: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var spec map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	// Spot-check the spec mentions our endpoints + our error envelope.
	bs, _ := json.Marshal(spec)
	for _, want := range []string{
		"/v1/auth/register",
		"/v1/auth/login",
		"/v1/users/me",
		"/v1/users/me/notifications",
		"/v1/friends",
		"/v1/conversations",
		"ErrorResponse",
	} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("openapi spec missing %q", want)
		}
	}
}

func TestRouter_DocsUI(t *testing.T) {
	t.Parallel()
	srv, c, _ := productionLikeServer(t)
	resp, err := c.Get(srv.URL + "/v1/docs/index.html")
	if err != nil {
		t.Fatalf("GET /v1/docs/index.html: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(strings.ToLower(string(body)), "swagger") {
		t.Errorf("body should contain 'swagger': %s", truncate(body, 400))
	}
}

func TestRouter_RequestIDEcho(t *testing.T) {
	t.Parallel()
	srv, c, _ := productionLikeServer(t)
	resp, err := c.Get(srv.URL + "/v1/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.Header.Get("X-Request-ID") == "" {
		t.Errorf("X-Request-ID header is empty")
	}
}

func TestRouter_Register_End2End(t *testing.T) {
	t.Parallel()
	srv, c, _ := productionLikeServer(t)
	body := strings.NewReader(`{"username":"smoke12345","email":"smoke12345@x.test","display_name":"Smoke","password":"Password123!"}`)
	resp, err := c.Post(srv.URL+"/v1/auth/register", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
	// Subsequent GET /v1/auth/me must succeed via the cookie jar.
	resp2, err := c.Get(srv.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	t.Cleanup(func() { _ = resp2.Body.Close() })
	if resp2.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp2.Body)
		t.Fatalf("/me status=%d body=%s", resp2.StatusCode, rb)
	}
}

func TestRouter_RequiresAuth_OnUsersList(t *testing.T) {
	t.Parallel()
	srv, c, _ := productionLikeServer(t)
	resp, err := c.Get(srv.URL + "/v1/users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusUnauthorized {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, rb)
	}
}

// truncate returns the first n bytes of b for log-friendly error output.
func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
