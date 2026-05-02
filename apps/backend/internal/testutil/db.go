package testutil

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // register the "pgx" database/sql driver pgtestdb opens
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
)

// migrationsDir resolves the absolute path to the repository's migrations/
// directory at compile time so a test running from any package finds the
// same set of SQL files.
func migrationsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile = <repo>/apps/backend/internal/testutil/db.go
	// migrations sit four levels up.
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "migrations")
}

// dsnToPGTestDBConfig adapts a postgres DSN (the format StartPostgres returns)
// into pgtestdb's structured Config. pgtestdb needs the parts split out
// because it builds per-test DB connection strings on its own.
func dsnToPGTestDBConfig(t *testing.T, dsn string) pgtestdb.Config {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("dsnToPGTestDBConfig: parse %q: %v", dsn, err)
	}
	password, _ := u.User.Password()
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	return pgtestdb.Config{
		DriverName: "pgx",
		Host:       host,
		Port:       port,
		User:       u.User.Username(),
		Password:   password,
		Database:   strings.TrimPrefix(u.Path, "/"),
		Options:    u.RawQuery,
	}
}

// NewTestDB returns a *pgxpool.Pool pointing at a freshly-cloned per-test
// database. The template DB is created (and migrations applied) once per
// test binary; every call after that gets its own clone in <50ms. The
// underlying postgres container is the singleton from StartPostgres, so
// callers don't need to start one explicitly.
//
// Use this in repository / service tests that need a real DB. The pool's
// Close is registered with t.Cleanup; do not call Close yourself.
func NewTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := StartPostgres(t)
	conf := dsnToPGTestDBConfig(t, dsn)

	// goosemigrator's internal fs.Sub call rejects absolute paths, so we hand
	// it an os.DirFS rooted at the migrations dir and pass "." as the relative
	// path inside that root.
	migrator := goosemigrator.New(".", goosemigrator.WithFS(os.DirFS(migrationsDir())))

	// Custom returns the per-test Config (pgtestdb has already created the DB
	// from the template). We then open our own pgxpool against it because the
	// rest of the codebase is pgx-native.
	cloneConf := pgtestdb.Custom(t, conf, migrator)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, cloneConf.URL())
	if err != nil {
		t.Fatalf("NewTestDB: open pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("NewTestDB: ping per-test DB: %v", err)
	}
	return pool
}
