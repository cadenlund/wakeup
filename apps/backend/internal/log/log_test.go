package log_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	wlog "github.com/cadenlund/wakeup/apps/backend/internal/log"
)

func TestParseLevel_Canonical(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{" info ", slog.LevelInfo}, // surrounding whitespace is trimmed
		{"\tDebug\n", slog.LevelDebug},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			lvl, ok := wlog.ParseLevel(tc.in)
			if !ok {
				t.Fatalf("ParseLevel(%q): expected recognized=true", tc.in)
			}
			if lvl != tc.expected {
				t.Fatalf("ParseLevel(%q) = %v, want %v", tc.in, lvl, tc.expected)
			}
		})
	}
}

func TestParseLevel_UnknownDefaultsToInfo(t *testing.T) {
	t.Parallel()
	lvl, ok := wlog.ParseLevel("verbose")
	if ok {
		t.Fatal("expected ok=false for unknown level")
	}
	if lvl != slog.LevelInfo {
		t.Fatalf("unknown level should default to info, got %v", lvl)
	}
}

func TestNewWithWriter_EmitsJSONAtLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := wlog.NewWithWriter("info", &buf)

	logger.Debug("should be filtered")    // below info — not emitted
	logger.Info("hello", "user_id", "u1") // emitted
	logger.Error("boom", "code", 500)     // emitted

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 emitted lines, got %d: %s", len(lines), buf.String())
	}

	// Each line must be valid JSON with the right level + fields.
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("not JSON: %q (%v)", line, err)
		}
		if _, ok := entry["time"]; !ok {
			t.Errorf("missing 'time' in %s", line)
		}
		if _, ok := entry["level"]; !ok {
			t.Errorf("missing 'level' in %s", line)
		}
	}
}

func TestNewWithWriter_UnknownLevelEmitsWarning(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_ = wlog.NewWithWriter("verbose", &buf) // typo'd level

	out := buf.String()
	if !strings.Contains(out, "unrecognized level") {
		t.Fatalf("expected unrecognized-level warning, got: %s", out)
	}
	// Must be at WARN level (not just an info-level note).
	if !strings.Contains(out, `"level":"WARN"`) {
		t.Fatalf("warning should be at WARN level, got: %s", out)
	}
}

func TestNew_DefaultsToInfo(t *testing.T) {
	t.Parallel()
	// Smoke test that New() with empty level builds a working logger.
	// Output goes to stdout — we don't capture it; we just confirm no panic.
	logger := wlog.New("")
	logger.Info("ok")
}
