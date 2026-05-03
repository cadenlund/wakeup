package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
)

// errorWriter is the surface Recovery uses to render the §4.4 envelope.
// Decoupled from internal/handler/http to avoid an import cycle (the
// handler package depends on middleware via main.go wiring).
type errorWriter func(w http.ResponseWriter, r *http.Request, err error)

// Capturer is the slice of the Sentry SDK Recovery needs: capture an
// error with tags. Defining the interface here lets recovery accept
// either a real Sentry client or the testutil.SentryRecorder fake.
// Pass nil at construction (when SENTRY_DSN is empty in dev) to
// disable capture without forcing the caller to write a no-op stub.
type Capturer interface {
	Capture(err error, tags map[string]string)
}

// RecoveryConfig packages the optional deps so callers don't keep
// widening the constructor as later phases (like §13.1 Sentry) add
// pieces. WriteError is required; Logger defaults to slog.Default()
// when nil; Sentry is a no-op when nil.
type RecoveryConfig struct {
	Logger     *slog.Logger
	WriteError errorWriter
	Sentry     Capturer
}

// Recovery catches panics in downstream handlers, logs the panic + stack
// via slog, captures to Sentry (when configured), and returns the §4.4
// INTERNAL envelope through writeError. writeError is required — the
// chain MUST emit a typed response, never plaintext (CodeRabbit caught
// the fallback path on PR #27).
//
// Recovery sits at the OUTSIDE of the chain (§4.7) so it sees panics
// from every other middleware too — including Logger and RequestID.
func Recovery(cfg RecoveryConfig) func(http.Handler) http.Handler {
	if cfg.WriteError == nil {
		panic("middleware.Recovery: nil WriteError")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler is the documented "I'm done, do not
				// log this" panic — chi's Renderer uses it. Re-panic so
				// the runtime closes the connection without spamming logs.
				if asErr, ok := rec.(error); ok && errors.Is(asErr, http.ErrAbortHandler) {
					panic(rec)
				}
				stack := debug.Stack()
				panicErr := fmt.Errorf("panic: %v", rec)
				// Recovery sits OUTSIDE RequestID per §4.7 ordering, so
				// the panicked goroutine's r.Context() doesn't carry the
				// id RequestID stamps further down the chain. RequestID
				// also echoes it on the response header, so use that as
				// the fallback — keeps the panic log / Sentry tag
				// correlated with the access log without rearranging the
				// middleware tower.
				reqID := RequestIDFromContext(r.Context())
				if reqID == "" {
					reqID = w.Header().Get(RequestIDHeader)
				}
				logger.ErrorContext(r.Context(), "panic recovered",
					slog.String("request_id", reqID),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", rec),
					slog.String("stack", string(stack)),
				)
				if cfg.Sentry != nil {
					cfg.Sentry.Capture(panicErr, map[string]string{
						"request_id": reqID,
						"method":     r.Method,
						"path":       r.URL.Path,
					})
				}
				cfg.WriteError(w, r, apierror.Internal("internal error").WithCause(panicErr))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
