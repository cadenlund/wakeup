package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cadenlund/wakeup/apps/backend/internal/config"
)

const (
	testDB    = "postgres://wakeup:wakeup@localhost:5432/wakeup?sslmode=disable"
	testRedis = "redis://localhost:6379/0"
)

func TestLoad_FromEnvOnly(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(config.LoadOpts{
		Environ: []string{
			"DATABASE_URL=" + testDB,
			"REDIS_URL=" + testRedis,
			"S3_FORCE_PATH_STYLE=true",
			"CORS_ALLOWED_ORIGINS=http://a.example, http://b.example",
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != testDB {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, testDB)
	}
	if cfg.RedisURL != testRedis {
		t.Fatalf("RedisURL = %q, want %q", cfg.RedisURL, testRedis)
	}
	if !cfg.S3ForcePathStyle {
		t.Fatal("S3ForcePathStyle = false, want true (env value 'true' should parse as bool)")
	}
	gotOrigins := cfg.CORSOriginList()
	wantOrigins := []string{"http://a.example", "http://b.example"}
	if !reflect.DeepEqual(gotOrigins, wantOrigins) {
		t.Fatalf("CORSOriginList = %v, want %v", gotOrigins, wantOrigins)
	}
}

func TestLoad_AppliesDefaultsWhenEnvUnset(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(config.LoadOpts{
		Environ: []string{
			"DATABASE_URL=" + testDB,
			"REDIS_URL=" + testRedis,
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "local" {
		t.Fatalf("Env default = %q, want %q", cfg.Env, "local")
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel default = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr default = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.SessionDomain != "localhost" {
		t.Fatalf("SessionDomain default = %q, want %q", cfg.SessionDomain, "localhost")
	}
}

func TestLoad_EnvOverridesDotenvFile(t *testing.T) {
	t.Parallel()
	envPath := filepath.Join(t.TempDir(), "test.env")
	body := "DATABASE_URL=postgres://from-file/db?sslmode=disable\n" +
		"REDIS_URL=redis://from-file/0\n" +
		"LOG_LEVEL=warn\n"
	if err := os.WriteFile(envPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := config.Load(config.LoadOpts{
		EnvFilePath: envPath,
		Environ:     []string{"LOG_LEVEL=debug"}, // env wins over file
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want %q (env should override file)", cfg.LogLevel, "debug")
	}
	if cfg.DatabaseURL != "postgres://from-file/db?sslmode=disable" {
		t.Fatalf("DatabaseURL not loaded from file: got %q", cfg.DatabaseURL)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	t.Parallel()
	// REDIS_URL set but DATABASE_URL not — should fail listing the missing field.
	_, err := config.Load(config.LoadOpts{
		Environ: []string{"REDIS_URL=" + testRedis},
	})
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("error should mention missing field: %v", err)
	}
	if strings.Contains(err.Error(), "REDIS_URL") {
		t.Fatalf("error should not flag REDIS_URL as missing: %v", err)
	}
}

func TestLoad_NonExistentEnvFileIsNotFatal(t *testing.T) {
	t.Parallel()
	// t.TempDir() gives a path that exists but the file inside doesn't.
	missingPath := filepath.Join(t.TempDir(), "missing.env")
	cfg, err := config.Load(config.LoadOpts{
		EnvFilePath: missingPath,
		Environ:     []string{"DATABASE_URL=" + testDB, "REDIS_URL=" + testRedis},
	})
	if err != nil {
		t.Fatalf("missing .env should not be fatal: %v", err)
	}
	if cfg.DatabaseURL != testDB {
		t.Fatalf("env-only load failed: got %q", cfg.DatabaseURL)
	}
}

func TestCORSOriginList_HandlesEdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"http://a", []string{"http://a"}},
		{"http://a,http://b", []string{"http://a", "http://b"}},
		{" http://a , , http://b ", []string{"http://a", "http://b"}},
		{",,,", nil},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			c := &config.Config{CORSAllowedOrigins: tc.raw}
			got := c.CORSOriginList()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("CORSOriginList(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
