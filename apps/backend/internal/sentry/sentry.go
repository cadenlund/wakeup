// Package sentry is a thin wrapper around the upstream sentry-go SDK
// per §13.1. The wrapper exists for two reasons:
//
//   - Decouples middleware/handler/service code from the Sentry SDK
//     surface — they depend only on a small `Capture(err, tags)`
//     contract (mw.Capturer / similar interfaces), so swapping the
//     transport (or replacing it with a no-op in dev) needs no
//     downstream code changes.
//   - Centralizes init/flush so cmd/server can hook lifecycle once
//     (Init at startup, Flush on shutdown) without spreading the SDK
//     across packages.
//
// In local/test environments where SENTRY_DSN is empty, cmd/server
// passes nil to the recovery middleware instead of constructing a
// Client — there's no global state to clean up in that path.
package sentry

import (
	"errors"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

// Client wraps a sentry-go Hub. Callers depend on the small
// `Capture(err, tags)` interface; the production wiring just routes to
// the upstream SDK while a noop fake (testutil.SentryRecorder) routes to
// an in-memory slice for assertions.
type Client struct {
	hub *sentry.Hub
}

// Config is the input to New. DSN is required (callers wrap nil-DSN
// behaviour in their own dev / production fork — see cmd/server's
// buildSentry).
type Config struct {
	DSN         string
	Environment string
	// Release is the git SHA / version tag stamped on every event so
	// triage in the Sentry UI can attribute issues to a deploy.
	Release string
}

// New initialises the Sentry SDK and returns a Client wrapping the
// shared Hub. A blank DSN errors — the dev fork is the caller's
// responsibility (consistent with buildMailer / buildPusher).
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("sentry: Config.DSN is required")
	}
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:         cfg.DSN,
		Environment: cfg.Environment,
		Release:     cfg.Release,
		// EnableTracing is off by default — Sentry billing is
		// per-event, so leaving distributed tracing for a later
		// milestone keeps the bill predictable.
		AttachStacktrace: true,
	}); err != nil {
		return nil, err
	}
	return &Client{hub: sentry.CurrentHub().Clone()}, nil
}

// Capture sends err to Sentry as an event. tags become indexed Sentry
// tags (request_id, method, path) so triage can filter on them.
//
// Capture never blocks longer than the SDK's internal queue allows; the
// upstream SDK drops events on a full queue rather than backpressuring
// the request goroutine. That's the right tradeoff for a recovery hook
// — observability mustn't break the response path.
func (c *Client) Capture(err error, tags map[string]string) {
	if c == nil || c.hub == nil {
		return
	}
	c.hub.WithScope(func(scope *sentry.Scope) {
		for k, v := range tags {
			scope.SetTag(k, v)
		}
		c.hub.CaptureException(err)
	})
}

// Flush blocks until queued events are sent, up to timeout. cmd/server
// calls this on graceful shutdown so SIGTERM doesn't drop in-flight
// captures. Returns false if the queue still had pending events when
// the timeout fired.
func (c *Client) Flush(timeout time.Duration) bool {
	if c == nil || c.hub == nil {
		return true
	}
	return c.hub.Flush(timeout)
}
