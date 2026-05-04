// Package testutil holds Go-test infrastructure shared across the backend:
// testcontainers helpers (this file), pgtestdb wiring, the HTTP/WS test
// harness, and aggregate fixtures. Production code MUST NOT import this
// package — its API exists to be used from `*_test.go` files only.
package testutil

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// MinIOAccessKey / MinIOSecretKey are the static credentials shared across
// every test that talks to the MinIO container started by StartMinIO. Tests
// build their S3 client using these constants — StartMinIO returns only the
// endpoint URL.
const (
	MinIOAccessKey = "minioadmin"
	MinIOSecretKey = "minioadmin"
)

var (
	pgOnce    sync.Once
	pgDSN     string
	pgErr     error
	redisOnce sync.Once
	redisURL  string
	redisErr  error
	minioOnce sync.Once
	minioURL  string
	minioErr  error
)

const containerStartTimeout = 90 * time.Second

// StartPostgres ensures a postgres:16 container is running and returns a DSN
// pointing at it (sslmode=disable). The container is created once per test
// binary (sync.Once) and reused by every caller; testcontainers' Ryuk reaper
// kills it when the process exits. Pair with pgtestdb (§12.2) for per-test
// schema isolation rather than spinning a new container per test.
func StartPostgres(t *testing.T) string {
	t.Helper()
	pgOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
		defer cancel()
		c, err := tcpostgres.Run(ctx, "postgres:16",
			tcpostgres.WithDatabase("wakeup"),
			tcpostgres.WithUsername("wakeup"),
			tcpostgres.WithPassword("wakeup"),
			testcontainers.WithWaitStrategy(
				// pg_isready returns ready twice during boot — the second is the real one.
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(containerStartTimeout),
			),
		)
		if err != nil {
			pgErr = fmt.Errorf("StartPostgres: run container: %w", err)
			return
		}
		dsn, err := c.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			pgErr = fmt.Errorf("StartPostgres: dsn: %w", err)
			return
		}
		pgDSN = dsn
	})
	if pgErr != nil {
		t.Fatalf("%v (is Docker running?)", pgErr)
	}
	return pgDSN
}

// StartRedis ensures a redis:7 container is running and returns its
// connection string (redis://host:port). One real Redis per test binary;
// miniredis is intentionally NOT used because §12.7 multi-instance pubsub
// tests need a real cross-process broker.
func StartRedis(t *testing.T) string {
	t.Helper()
	redisOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
		defer cancel()
		c, err := tcredis.Run(ctx, "redis:7")
		if err != nil {
			redisErr = fmt.Errorf("StartRedis: run container: %w", err)
			return
		}
		url, err := c.ConnectionString(ctx)
		if err != nil {
			redisErr = fmt.Errorf("StartRedis: connection string: %w", err)
			return
		}
		redisURL = url
	})
	if redisErr != nil {
		t.Fatalf("%v (is Docker running?)", redisErr)
	}
	return redisURL
}

// StartMinIO ensures a minio container is running and returns the HTTP
// endpoint URL (no scheme prefix on the bare ConnectionString — caller
// prepends "http://"). Credentials are MinIOAccessKey / MinIOSecretKey.
// Bucket creation is the caller's responsibility (use the s3 client from
// internal/objectstore once it lands in §16 milestone 2.7).
func StartMinIO(t *testing.T) string {
	t.Helper()
	minioOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
		defer cancel()
		// Pin to a specific MinIO release rather than :latest. The OSS image has
		// stopped receiving new releases (archived late 2025); the last published
		// tag is RELEASE.2025-09-07T16-13-09Z. Pinning insulates CI from any
		// future tag drift while we evaluate alternatives.
		c, err := tcminio.Run(ctx, "minio/minio:RELEASE.2025-09-07T16-13-09Z",
			tcminio.WithUsername(MinIOAccessKey),
			tcminio.WithPassword(MinIOSecretKey),
		)
		if err != nil {
			minioErr = fmt.Errorf("StartMinIO: run container: %w", err)
			return
		}
		url, err := c.ConnectionString(ctx)
		if err != nil {
			minioErr = fmt.Errorf("StartMinIO: connection string: %w", err)
			return
		}
		minioURL = "http://" + url
		// tcminio.Run waits for the container, but the MinIO process inside
		// can take another ~second to finish initialization — an early
		// CreateBucket can hit XMinioServerNotInitialized under heavy
		// parallel load. Poll /minio/health/live until 200 OK or timeout
		// before returning so callers see a ready server.
		minioErr = waitForMinIOReady(minioURL)
	})
	if minioErr != nil {
		t.Fatalf("%v (is Docker running?)", minioErr)
	}
	return minioURL
}

// waitForMinIOReady polls /minio/health/live until 200 OK. The endpoint
// is documented at https://min.io/docs/minio/linux/operations/monitoring/healthcheck-probe.html
// and returns 200 only after MinIO has finished initializing — i.e. it's
// the right gate to clear the XMinioServerNotInitialized error window.
func waitForMinIOReady(baseURL string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/minio/health/live")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("StartMinIO: server did not become ready within 30s")
}
