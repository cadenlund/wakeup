// Package config loads typed runtime configuration from .env files and
// process environment variables (env wins over .env, defaults fill in the
// rest). Used by cmd/server/main.go and any test that needs a populated
// Config without actually setting OS env vars.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/dotenv"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Config is the strongly-typed view of the variables documented in
// .env.example. Tags use the lowercase form so koanf can map env vars like
// DATABASE_URL → database_url → field DatabaseURL via its Unmarshal step.
type Config struct {
	Env           string `koanf:"env"`            // local | staging | production
	LogLevel      string `koanf:"log_level"`      // debug | info | warn | error
	HTTPAddr      string `koanf:"http_addr"`      // listen address, e.g. ":8080"
	SessionDomain string `koanf:"session_domain"` // domain attribute for session cookie

	DatabaseURL string `koanf:"database_url"`
	RedisURL    string `koanf:"redis_url"`

	S3Endpoint       string `koanf:"s3_endpoint"`
	S3Region         string `koanf:"s3_region"`
	S3AccessKey      string `koanf:"s3_access_key"`
	S3SecretKey      string `koanf:"s3_secret_key"`
	S3Bucket         string `koanf:"s3_bucket"`
	S3ForcePathStyle bool   `koanf:"s3_force_path_style"` // true for MinIO

	ResendAPIKey    string `koanf:"resend_api_key"`
	ResendFromEmail string `koanf:"resend_from_email"`

	LiveKitURL       string `koanf:"livekit_url"`
	LiveKitAPIKey    string `koanf:"livekit_api_key"`
	LiveKitAPISecret string `koanf:"livekit_api_secret"`
	// RoomLoneKickAfter is the §10.3 Discord-style timeout: when a
	// participant is alone in a room for this long, the lone-kick
	// sweeper drops them. Stored as a Go duration string ("5m",
	// "30s") so koanf's plain string unmarshal works; main.go calls
	// RoomLoneKickAfterDuration to parse. Empty / "0" disables.
	RoomLoneKickAfter string `koanf:"room_lone_kick_after"`

	ExpoAccessToken string `koanf:"expo_access_token"`

	SentryDSN         string `koanf:"sentry_dsn"`
	SentryEnvironment string `koanf:"sentry_environment"`

	// Raw comma-joined value as it appears in env. Use CORSOriginList for the
	// parsed slice so callers don't have to split.
	CORSAllowedOrigins string `koanf:"cors_allowed_origins"`
}

// RoomLoneKickAfterDuration parses the §10.3 lone-user kick timeout
// from its string env form. The koanf defaults populate this with
// "5m" when the env is absent, so an empty value here means the
// operator deliberately blanked it; treat that as "disable" (zero
// duration). Returns 0 + an error when the value can't be parsed so
// a fat-finger in the .env file fails at boot rather than silently
// disabling the feature. Use Go's standard duration syntax:
// "5m", "30s", "1h".
func (c *Config) RoomLoneKickAfterDuration() (time.Duration, error) {
	raw := strings.TrimSpace(c.RoomLoneKickAfter)
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: ROOM_LONE_KICK_AFTER %q: %w", raw, err)
	}
	return d, nil
}

// CORSOriginList splits the comma-joined CORSAllowedOrigins env value into a
// trimmed []string and drops empty entries. Returns nil — not an empty slice
// — when there are no usable entries, so callers can use a single nil check.
func (c *Config) CORSOriginList() []string {
	if c.CORSAllowedOrigins == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(c.CORSAllowedOrigins, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Defaults applied below before anything else loads. Optional vars are blank
// so the validation pass can see "missing" vs "user provided empty."
//
// room_lone_kick_after is the §10.3 lone-user kick timeout. The default
// is applied here (not in room.New) so an explicit "0" or "-1s" in the
// env stays meaningful — zero/negative disables the feature, while an
// absent env var falls through to the documented 5m.
var defaults = map[string]any{
	"env":                  "local",
	"log_level":            "info",
	"http_addr":            ":8080",
	"session_domain":       "localhost",
	"room_lone_kick_after": "5m",
}

// LoadOpts customizes Load. Production callers pass the zero value (which
// reads .env from disk and shells out to os.Environ); tests pass an explicit
// Environ slice so they can run with t.Parallel() without mutating real OS
// env state.
type LoadOpts struct {
	// EnvFilePath is the .env file to read (optional). Pass "" to skip.
	EnvFilePath string

	// Environ is an explicit "KEY=VALUE" slice (matches os.Environ shape).
	// If nil (the production default), os.Environ is used. Tests pass a
	// fixed list to avoid t.Setenv (which is incompatible with t.Parallel).
	Environ []string
}

// Load builds a Config in the documented precedence:
//
//  1. Hardcoded defaults for the small set of always-defaulted fields.
//  2. .env file at opts.EnvFilePath if it exists.
//  3. Environment variables (opts.Environ or os.Environ if nil). These
//     override .env and defaults.
//
// After loading, Validate() runs and returns an error listing any missing
// required fields.
func Load(opts LoadOpts) (*Config, error) {
	k := koanf.New(".")

	// Step 1 — defaults.
	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return nil, fmt.Errorf("config: load defaults: %w", err)
	}

	// Step 2 — .env file (optional).
	if opts.EnvFilePath != "" {
		if _, err := os.Stat(opts.EnvFilePath); err == nil {
			// dotenv.ParserEnv lowercases the key so DATABASE_URL → database_url.
			if err := k.Load(file.Provider(opts.EnvFilePath),
				dotenv.ParserEnv("", ".", strings.ToLower)); err != nil {
				return nil, fmt.Errorf("config: parse %s: %w", opts.EnvFilePath, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: stat %s: %w", opts.EnvFilePath, err)
		}
	}

	// Step 3 — env vars (overrides everything above). The TransformFunc
	// lowercases keys; values pass through unchanged so koanf's struct
	// unmarshal handles bool/int conversion.
	envOpt := env.Opt{
		TransformFunc: func(k, v string) (string, any) {
			return strings.ToLower(k), v
		},
	}
	if opts.Environ != nil {
		envOpt.EnvironFunc = func() []string { return opts.Environ }
	}
	if err := k.Load(env.Provider(".", envOpt), nil); err != nil {
		return nil, fmt.Errorf("config: load env: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the small set of fields the server can't run without.
// External-service credentials (Resend, Expo, Sentry, LiveKit) are NOT
// required at boot — the corresponding feature simply fails when the API
// is invoked, which is the same behavior as a misconfigured remote.
func (c *Config) Validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.RedisURL == "" {
		missing = append(missing, "REDIS_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: missing required env: %s", strings.Join(missing, ", "))
	}
	return nil
}
