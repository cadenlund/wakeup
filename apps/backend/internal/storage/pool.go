package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig is the input for opening a *pgxpool.Pool. DatabaseURL is required;
// every other field is optional and falls through to pgxpool's own defaults
// when zero (MaxConns defaults to 4×CPU, MinConns to 0, etc.).
type PoolConfig struct {
	DatabaseURL     string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// NewPool opens a connection pool against DatabaseURL and verifies connectivity
// with a Ping before returning. The caller owns the pool's lifecycle and must
// invoke Close at shutdown.
func NewPool(ctx context.Context, c PoolConfig) (*pgxpool.Pool, error) {
	if c.DatabaseURL == "" {
		return nil, errors.New("storage: PoolConfig.DatabaseURL is required")
	}
	// Reject negative knobs up front so a misconfigured env var fails at boot
	// rather than silently being ignored at runtime.
	if c.MaxConns < 0 {
		return nil, fmt.Errorf("storage: PoolConfig.MaxConns must be >= 0, got %d", c.MaxConns)
	}
	if c.MinConns < 0 {
		return nil, fmt.Errorf("storage: PoolConfig.MinConns must be >= 0, got %d", c.MinConns)
	}
	if c.MaxConnLifetime < 0 {
		return nil, fmt.Errorf("storage: PoolConfig.MaxConnLifetime must be >= 0, got %s", c.MaxConnLifetime)
	}
	if c.MaxConnIdleTime < 0 {
		return nil, fmt.Errorf("storage: PoolConfig.MaxConnIdleTime must be >= 0, got %s", c.MaxConnIdleTime)
	}

	cfg, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("storage: parse pool config: %w", err)
	}
	if c.MaxConns > 0 {
		cfg.MaxConns = c.MaxConns
	}
	if c.MinConns > 0 {
		cfg.MinConns = c.MinConns
	}
	if c.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = c.MaxConnLifetime
	}
	if c.MaxConnIdleTime > 0 {
		cfg.MaxConnIdleTime = c.MaxConnIdleTime
	}
	// pgxpool itself doesn't enforce this; pool startup will eventually fail in
	// confusing ways if MinConns > MaxConns, so catch it here.
	if cfg.MinConns > cfg.MaxConns {
		return nil, fmt.Errorf(
			"storage: PoolConfig.MinConns (%d) must be <= MaxConns (%d)",
			cfg.MinConns, cfg.MaxConns,
		)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("storage: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: ping pool: %w", err)
	}
	return pool, nil
}

// HealthCheck pings the pool with the caller's context. Wired into /v1/readyz
// (§13.13.2) so the readiness probe fails when the database is unreachable.
// Pass a context with a short deadline (≤2s) at the call site so a stuck DB
// can't hang the probe.
func HealthCheck(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("storage: HealthCheck called with nil pool")
	}
	return pool.Ping(ctx)
}
