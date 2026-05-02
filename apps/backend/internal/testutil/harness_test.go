package testutil_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil/fixtures"
)

func TestNew_BasicShape(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)

	if h.Server == nil {
		t.Fatal("Server is nil")
	}
	if !strings.HasPrefix(h.Server.URL, "http://") {
		t.Fatalf("Server.URL = %q, want http:// prefix", h.Server.URL)
	}
	if h.DB == nil {
		t.Fatal("DB is nil")
	}
	if h.Redis == nil {
		t.Fatal("Redis is nil")
	}
	if h.Mailer == nil || h.Pusher == nil || h.Storage == nil || h.Sentry == nil {
		t.Fatal("one or more fakes are nil")
	}

	// Both DB and Redis are usable: ping each.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.DB.Ping(ctx); err != nil {
		t.Fatalf("DB.Ping: %v", err)
	}
	if err := h.Redis.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis.Ping: %v", err)
	}
}

// New must be safe to call from many tests in parallel — each gets isolated
// state so writes in one harness can't bleed into another.
func TestNew_ParallelIsolation(t *testing.T) {
	t.Parallel()

	const N = 4
	harnesses := make([]*testutil.Harness, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			// Subtests aren't necessary; we just need each goroutine to
			// produce a Harness without races. testing.T is safe across
			// goroutines for Helper/Cleanup but not for FailNow — keep
			// failures local.
			harnesses[i] = testutil.New(t)
		}(i)
	}
	wg.Wait()

	// Each harness must point at a different test server and a different
	// underlying database (proven by inserting in one and not seeing it in
	// the others).
	urls := map[string]struct{}{}
	for _, h := range harnesses {
		urls[h.Server.URL] = struct{}{}
	}
	if len(urls) != N {
		t.Fatalf("expected %d distinct test server URLs, got %d", N, len(urls))
	}

	// Insert a fixture user into harness[0]; harness[1] must not see it.
	user0 := fixtures.MakeUser(t, harnesses[0].DB)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	if err := harnesses[1].DB.QueryRow(ctx,
		"SELECT count(*) FROM users WHERE id = $1", user0.ID,
	).Scan(&n); err != nil {
		t.Fatalf("count on h1: %v", err)
	}
	if n != 0 {
		t.Fatalf("isolation broken: h1 sees user from h0")
	}
}

func TestHTTPClient_HasCookieJar(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	if c.Jar == nil {
		t.Fatal("HTTPClient should return a client with a cookie jar")
	}
}

// AuthClient/AdminClient/WSDial deliberately panic until later phases (per
// the milestone-1.9 plan documented in harness.go). Recovering the panic
// here lets us assert the message is informative — a future agent reading
// "wire me in Phase X" knows where to look.
func TestAuthClient_PanicsWithMilestonePointer(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected AuthClient to panic until Phase 3.6")
		}
		msg := toString(r)
		if !strings.Contains(msg, "Phase 3.6") {
			t.Fatalf("panic should reference Phase 3.6, got: %s", msg)
		}
	}()
	_, _ = h.AuthClient(t)
}

func TestMakeUser_PersistsRow(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	u := fixtures.MakeUser(t, h.DB,
		fixtures.WithUsername("caden-test"),
		fixtures.WithDisplayName("Caden T."),
		fixtures.WithRole("admin"),
		fixtures.WithColorScheme("dark"),
	)
	if u.Username != "caden-test" || u.DisplayName != "Caden T." {
		t.Fatalf("fields not persisted: %+v", u)
	}
	if u.Role != "admin" || u.ColorScheme != "dark" {
		t.Fatalf("optional fields not applied: %+v", u)
	}
	if u.DeletedAt != nil {
		t.Fatalf("DeletedAt should be nil on a non-soft-deleted user, got %v", u.DeletedAt)
	}
}

func TestMakeUser_SoftDeleted(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	u := fixtures.MakeUser(t, h.DB, fixtures.WithSoftDeleted())
	if u.DeletedAt == nil {
		t.Fatal("WithSoftDeleted should set DeletedAt")
	}
}

// Fakes round-trip what they capture so handler tests can assert on them.
func TestFakeMailer_Captures(t *testing.T) {
	t.Parallel()
	m := &testutil.FakeMailer{}
	if err := m.SendPasswordReset(context.Background(), "to@example.com", "tok"); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}
	if len(m.Sent) != 1 || m.Sent[0].To != "to@example.com" || m.Sent[0].Token != "tok" {
		t.Fatalf("Sent capture wrong: %+v", m.Sent)
	}
}

// toString turns a recover() value into a string for inspection.
func toString(v any) string {
	switch s := v.(type) {
	case error:
		return s.Error()
	case string:
		return s
	default:
		return ""
	}
}

// Compile-time guard that http.Client is the type AuthClient eventually
// returns. Not load-bearing today — silences the unused-import linter for
// net/http while AuthClient is stubbed.
var _ http.Client
