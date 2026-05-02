package testutil_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// These tests boot real containers via testcontainers — they require Docker
// available on the host. CI's ubuntu-latest runner has Docker pre-installed.
// They share singleton containers (sync.Once) so the boot cost is paid once
// per package-test-binary, not once per test.

func TestStartPostgres_ReturnsConnectableDSN(t *testing.T) {
	t.Parallel()
	dsn := testutil.StartPostgres(t)
	if dsn == "" {
		t.Fatal("expected non-empty DSN")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestStartPostgres_ReusesContainer(t *testing.T) {
	t.Parallel()
	a := testutil.StartPostgres(t)
	b := testutil.StartPostgres(t)
	if a != b {
		t.Fatalf("expected sync.Once-cached DSN, got two: %q vs %q", a, b)
	}
}

func TestStartRedis_ReturnsConnectableURL(t *testing.T) {
	t.Parallel()
	url := testutil.StartRedis(t)
	if !strings.HasPrefix(url, "redis://") {
		t.Fatalf("expected redis:// URL, got %q", url)
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestStartMinIO_ReturnsHealthyEndpoint(t *testing.T) {
	t.Parallel()
	endpoint := testutil.StartMinIO(t)
	if !strings.HasPrefix(endpoint, "http://") {
		t.Fatalf("expected http:// URL, got %q", endpoint)
	}

	// MinIO exposes /minio/health/live (200) when ready. Avoid pulling in the
	// full S3 client just to prove connectivity in this test.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/minio/health/live", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MinIO health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MinIO health returned %d", resp.StatusCode)
	}
}
