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

// Recovery catches panics in downstream handlers, logs the panic + stack
// via slog, and returns the §4.4 INTERNAL envelope through writeError.
// writeError is required — the chain MUST emit a typed response, never
// plaintext (CodeRabbit caught the fallback path on PR #27).
//
// Recovery sits at the OUTSIDE of the chain (§4.7) so it sees panics
// from every other middleware too — including Logger and RequestID.
func Recovery(logger *slog.Logger, writeError errorWriter) func(http.Handler) http.Handler {
	if writeError == nil {
		panic("middleware.Recovery: nil writeError")
	}
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
				logger.ErrorContext(r.Context(), "panic recovered",
					slog.String("request_id", RequestIDFromContext(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", rec),
					slog.String("stack", string(stack)),
				)
				writeError(w, r, apierror.Internal("internal error").
					WithCause(fmt.Errorf("panic: %v", rec)))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
