package main

import (
	"context"
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

	lkauth "github.com/livekit/protocol/auth"

	"github.com/cadenlund/wakeup/apps/backend/internal/config"
	httpapi "github.com/cadenlund/wakeup/apps/backend/internal/handler/http"
	wshandler "github.com/cadenlund/wakeup/apps/backend/internal/handler/ws"
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
	msgHandler, err := httpapi.NewMessageHandler(h.MsgSvc, h.AuthSvc, v)
	if err != nil {
		t.Fatalf("message handler: %v", err)
	}
	attHandler, err := httpapi.NewAttachmentHandler(h.AttSvc, h.AuthSvc)
	if err != nil {
		t.Fatalf("attachment handler: %v", err)
	}
	wsHandler, err := wshandler.NewHandler(wshandler.HandlerConfig{
		Hub: h.WSHub, Bridge: h.WSBridge, Broker: h.Broker,
		Auth: h.AuthSvc, Convs: h.ConvSvc,
		AllowedOrigins: []string{"*"},
		WriteError:     httpapi.WriteError,
	})
	if err != nil {
		t.Fatalf("ws handler: %v", err)
	}
	presenceHandler, err := httpapi.NewPresenceHandler(h.PresenceSvc, h.UserSvc, h.AuthSvc, v)
	if err != nil {
		t.Fatalf("presence handler: %v", err)
	}
	roomHandler, err := httpapi.NewRoomHandler(h.RoomSvc, h.AuthSvc, v)
	if err != nil {
		t.Fatalf("room handler: %v", err)
	}
	deviceHandler, err := httpapi.NewDeviceHandler(h.DeviceSvc, h.AuthSvc, v)
	if err != nil {
		t.Fatalf("device handler: %v", err)
	}
	adminHandler, err := httpapi.NewAdminHandler(h.AdminSvc, h.AuthSvc, h.Sessions, v)
	if err != nil {
		t.Fatalf("admin handler: %v", err)
	}
	livekitWebhookHandler, err := httpapi.NewLiveKitWebhookHandler(
		h.RoomSvc, h.Broker,
		lkauth.NewSimpleKeyProvider(testutil.LiveKitDevAPIKey, testutil.LiveKitDevAPISecret),
		nil,
		httpapi.LiveKitWebhookHandlerConfig{
			Convs:         h.ConvRepo,
			Presence:      h.PresenceSvc,
			Notifications: h.NotificationSvc,
		},
	)
	if err != nil {
		t.Fatalf("livekit webhook handler: %v", err)
	}

	// Each test gets a unique rate-limit scope so parallel smoke tests
	// (all bound to 127.0.0.1, all sharing the testcontainer redis)
	// don't collide on the auth-tier per-IP bucket. Limits stay
	// production-shaped — only the keyspace differs.
	suffix := "-" + testutil.NextSuffix()
	router, err := buildRouter(routerDeps{
		Cfg:                   cfg,
		Logger:                slog.Default(),
		Pool:                  h.DB,
		Redis:                 h.Redis,
		Sessions:              h.Sessions,
		Limiter:               ratelimit.New(h.Redis),
		IdempotencyRepo:       h.IdempotencyRepo,
		UserSvc:               h.UserSvc,
		AuthSvc:               h.AuthSvc,
		NotifPrefSvc:          h.NotifPrefSvc,
		FriendSvc:             h.FriendSvc,
		ConvSvc:               h.ConvSvc,
		MsgSvc:                h.MsgSvc,
		AttSvc:                h.AttSvc,
		PresenceSvc:           h.PresenceSvc,
		RoomSvc:               h.RoomSvc,
		DeviceSvc:             h.DeviceSvc,
		AdminSvc:              h.AdminSvc,
		UserHandler:           userHandler,
		AuthHandler:           authHandler,
		FriendHandler:         friendHandler,
		ConversationHandler:   convHandler,
		MessageHandler:        msgHandler,
		AttachmentHandler:     attHandler,
		PresenceHandler:       presenceHandler,
		RoomHandler:           roomHandler,
		DeviceHandler:         deviceHandler,
		AdminHandler:          adminHandler,
		LiveKitWebhookHandler: livekitWebhookHandler,
		WSHandler:             wsHandler,
		RateLimitAuth:         rateLimitTier{Scope: "auth" + suffix, Limit: 10000, Window: time.Minute},
		RateLimitWrites:       rateLimitTier{Scope: "writes" + suffix, Limit: 10000, Window: time.Minute},
		RateLimitReads:        rateLimitTier{Scope: "reads" + suffix, Limit: 10000, Window: time.Minute},
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

// TestRouter_AdminSmoke is the §16 milestone 12.6 end-to-end flow run
// through the production middleware chain instead of a manual Swagger UI.
//
// It walks the full §8.7 path: register two users → SQL-promote one to
// admin → log in as admin → list users → impersonate the second user →
// verify /v1/auth/me returns the target with impersonated_by populated →
// confirm the §12.4 BLOCKED_DURING_IMPERSONATION guard fires on the four
// listed dangerous routes → end impersonation → verify /v1/auth/me
// returns the admin → assert audit log holds the bookend pair.
//
// This test doubles as the §12.4 wire-level check that I deferred from
// the admin-handler unit tests (the harness in handler/http
// intentionally doesn't re-implement the full router middleware tower).
func TestRouter_AdminSmoke(t *testing.T) {
	t.Parallel()
	srv, c, h := productionLikeServer(t)

	// 1. Register two users via the public /v1/auth/register endpoint.
	// Each registration leaves a fresh session cookie in the jar; we
	// reset the jar between calls so the second registration doesn't
	// inherit the first user's session.
	adminBody := `{"username":"smokeadmin","email":"smokeadmin@x.test","display_name":"Admin","password":"Password123!"}`
	smokeRegister(t, c, srv.URL, adminBody)
	smokeLogout(t, c, srv.URL)

	targetBody := `{"username":"smoketarget","email":"smoketarget@x.test","display_name":"Target","password":"Password123!"}`
	smokeRegister(t, c, srv.URL, targetBody)
	smokeLogout(t, c, srv.URL)

	// 2. Promote the admin user via direct SQL (the §12.6 spec uses SQL
	// for this, since the bootstrap admin has no upstream way to be
	// promoted). Look up the row by username so we don't depend on
	// register's response shape here.
	ctx := context.Background()
	tag, err := h.DB.Exec(ctx,
		"UPDATE users SET role = 'admin' WHERE username = 'smokeadmin'")
	if err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	// Exactly one row should be affected — the smokeadmin user we just
	// registered. A 0 means the username didn't match (test setup bug);
	// a >1 means a leak from a parallel test (impossible in pgtestdb,
	// but cheap to guard).
	if got := tag.RowsAffected(); got != 1 {
		t.Fatalf("promote admin: expected 1 row affected, got %d", got)
	}
	adminUser, err := h.UserRepo.GetByEmail(ctx, "smokeadmin@x.test")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	targetUser, err := h.UserRepo.GetByEmail(ctx, "smoketarget@x.test")
	if err != nil {
		t.Fatalf("lookup target: %v", err)
	}

	// 3. Log in as the admin (cookie jar now holds the admin session).
	loginBody := `{"identifier":"smokeadmin","password":"Password123!"}`
	smokeLogin(t, c, srv.URL, loginBody)

	// 4. GET /v1/admin/users should succeed and list both users.
	listResp, err := c.Get(srv.URL + "/v1/admin/users?limit=10")
	if err != nil {
		t.Fatalf("admin/users: %v", err)
	}
	t.Cleanup(func() { _ = listResp.Body.Close() })
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		t.Fatalf("admin/users status=%d body=%s", listResp.StatusCode, body)
	}
	listBody := decodeMap(t, listResp.Body)
	users, _ := listBody["data"].([]any)
	if len(users) < 2 {
		t.Fatalf("expected at least 2 users in admin list, got %d", len(users))
	}

	// 5. Impersonate the target.
	impURL := srv.URL + "/v1/admin/users/" + targetUser.ID.String() + "/impersonate"
	impResp, err := c.Post(impURL, "application/json", nil)
	if err != nil {
		t.Fatalf("impersonate: %v", err)
	}
	t.Cleanup(func() { _ = impResp.Body.Close() })
	if impResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(impResp.Body)
		t.Fatalf("impersonate status=%d body=%s", impResp.StatusCode, body)
	}
	impBody := decodeMap(t, impResp.Body)
	if impBody["id"] != targetUser.ID.String() {
		t.Errorf("impersonate returned id=%v, want target %v", impBody["id"], targetUser.ID)
	}
	imp, _ := impBody["impersonated_by"].(map[string]any)
	if imp == nil || imp["id"] != adminUser.ID.String() {
		t.Errorf("impersonated_by missing or wrong: %+v", impBody)
	}

	// 6. /v1/auth/me now returns the target with impersonated_by set.
	meResp1, err := c.Get(srv.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("/me during impersonation: %v", err)
	}
	t.Cleanup(func() { _ = meResp1.Body.Close() })
	me1 := decodeMap(t, meResp1.Body)
	if me1["id"] != targetUser.ID.String() {
		t.Errorf("/me during impersonation should return target, got %v", me1["id"])
	}
	if _, ok := me1["impersonated_by"].(map[string]any); !ok {
		t.Errorf("impersonated_by missing on /me: %+v", me1)
	}

	// 7. §12.4 dangerous-route guard. Each of these must surface
	// 403 BLOCKED_DURING_IMPERSONATION while the admin is impersonating.
	// Inline loop (not subtests): all four assertions share the cookie
	// jar's mid-flow impersonation state, and t.Run wouldn't add value
	// since they aren't parallelisable here anyway.
	guardCases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"DELETE /v1/users/me", http.MethodDelete, "/v1/users/me", ""},
		{"POST /v1/auth/logout-all", http.MethodPost, "/v1/auth/logout-all", ""},
		{
			"PATCH /v1/users/me/notifications",
			http.MethodPatch, "/v1/users/me/notifications",
			`{"direct_messages":false}`,
		},
		{
			"POST /v1/auth/password-reset/request",
			http.MethodPost, "/v1/auth/password-reset/request",
			`{"email":"smoketarget@x.test"}`,
		},
	}
	for _, route := range guardCases {
		req, err := http.NewRequest(route.method, srv.URL+route.path, strings.NewReader(route.body))
		if err != nil {
			t.Fatalf("%s: build request: %v", route.name, err)
		}
		if route.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", route.name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s status=%d body=%s, want 403", route.name, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "BLOCKED_DURING_IMPERSONATION") {
			t.Errorf("%s body should contain BLOCKED_DURING_IMPERSONATION code: %s",
				route.name, body)
		}
	}

	// 8. End impersonation.
	endResp, err := c.Post(srv.URL+"/v1/admin/impersonate/end", "application/json", nil)
	if err != nil {
		t.Fatalf("end impersonate: %v", err)
	}
	t.Cleanup(func() { _ = endResp.Body.Close() })
	if endResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(endResp.Body)
		t.Fatalf("end impersonate status=%d body=%s", endResp.StatusCode, body)
	}
	endBody := decodeMap(t, endResp.Body)
	if _, ok := endBody["impersonated_by"]; ok {
		t.Errorf("End response must not include impersonated_by: %+v", endBody)
	}

	// 9. /v1/auth/me back to the admin.
	meResp2, err := c.Get(srv.URL + "/v1/auth/me")
	if err != nil {
		t.Fatalf("/me after End: %v", err)
	}
	t.Cleanup(func() { _ = meResp2.Body.Close() })
	me2 := decodeMap(t, meResp2.Body)
	if me2["id"] != adminUser.ID.String() {
		t.Errorf("/me after End should return admin, got %v", me2["id"])
	}
	if _, ok := me2["impersonated_by"]; ok {
		t.Errorf("/me after End must not include impersonated_by: %+v", me2)
	}

	// 10. Audit log holds both bookends with actor_id = admin.
	auditResp, err := c.Get(srv.URL + "/v1/admin/audit?limit=20")
	if err != nil {
		t.Fatalf("admin/audit: %v", err)
	}
	t.Cleanup(func() { _ = auditResp.Body.Close() })
	if auditResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(auditResp.Body)
		t.Fatalf("admin/audit status=%d body=%s", auditResp.StatusCode, body)
	}
	auditBody := decodeMap(t, auditResp.Body)
	entries, _ := auditBody["data"].([]any)
	seen := map[string]bool{}
	for _, e := range entries {
		row, _ := e.(map[string]any)
		action, _ := row["action"].(string)
		seen[action] = true
		if action == "impersonate.started" || action == "impersonate.ended" {
			if row["actor_id"] != adminUser.ID.String() {
				t.Errorf("%s actor_id should be the admin (%v), got %v",
					action, adminUser.ID, row["actor_id"])
			}
		}
	}
	if !seen["impersonate.started"] || !seen["impersonate.ended"] {
		t.Errorf("audit log missing the bookend pair: %+v", seen)
	}
}

// --- smoke helpers -------------------------------------------------------

func smokeRegister(t *testing.T, c *http.Client, baseURL, body string) {
	t.Helper()
	resp, err := c.Post(baseURL+"/v1/auth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("register status=%d body=%s", resp.StatusCode, rb)
	}
}

func smokeLogin(t *testing.T, c *http.Client, baseURL, body string) {
	t.Helper()
	resp, err := c.Post(baseURL+"/v1/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status=%d body=%s", resp.StatusCode, rb)
	}
}

func smokeLogout(t *testing.T, c *http.Client, baseURL string) {
	t.Helper()
	resp, err := c.Post(baseURL+"/v1/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Logout is idempotent and always 204. A non-2xx here means the
	// session jar is in an unexpected state and the rest of the smoke
	// flow would run against the wrong session — fail immediately.
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("logout status=%d body=%s", resp.StatusCode, body)
	}
}

func decodeMap(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// truncate returns the first n bytes of b for log-friendly error output.
func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
