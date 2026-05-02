// Package log builds the project's slog.Logger. JSON-formatted output to the
// configured writer (stdout in production per §11 — Fly aggregates logs).
// Per-request enrichment (request_id, user_id, etc.) is the middleware's job;
// this package only sets the handler shape and level.
package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New builds a JSON-handler slog.Logger writing to os.Stdout at the level
// parsed from the given string ("debug" | "info" | "warn" | "error", any
// case). Unrecognized values fall back to info — startup logs that.
func New(level string) *slog.Logger {
	return NewWithWriter(level, os.Stdout)
}

// NewWithWriter is like New but redirects output to w. Useful in tests that
// need to assert the exact bytes the logger emits.
func NewWithWriter(level string, w io.Writer) *slog.Logger {
	lvl, ok := ParseLevel(level)
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})
	logger := slog.New(h)
	if !ok && level != "" {
		// One-shot warning so an operator who typo'd LOG_LEVEL sees it land in
		// their aggregator rather than silently getting info-level logs.
		logger.Warn("log: unrecognized level, defaulting to info", "value", level)
	}
	return logger
}

// ParseLevel maps a config-style level string to slog.Level. The bool reports
// whether the input was a recognized canonical level — callers can use it to
// surface a warning when an operator typos the value.
//
// "" is treated as recognized and resolves to info, matching koanf's default.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, true
	case "", "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}
