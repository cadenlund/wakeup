// Package storage holds the foundational database primitives shared across
// the backend: the DBTX interface (this file) and the pgx connection pool.
//
// DBTX is the single most load-bearing pattern in the codebase. Every
// repository depends on it; concrete *pgxpool.Pool and pgx.Tx both satisfy
// it, so a repository method runs identically inside or outside a
// transaction. See WAKEUP.md §4.2 for the full rationale.
package storage

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is the contract every repository takes — never a concrete *pgxpool.Pool.
// Both *pgxpool.Pool and pgx.Tx implement it, which is why a repository's
// methods work transparently inside or outside a transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}
