package middleware

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Logger emits one structured slog line per request with the §4.7 fields:
// method, path, status, duration_ms, request_id, user_id (when authed).
//
// Wrap downstream so we can record the response status; we only spy on
// status writes, not the body, so we don't add a meaningful allocation
// per request.
func Logger(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			}
			if rid := RequestIDFromContext(r.Context()); rid != "" {
				attrs = append(attrs, slog.String("request_id", rid))
			}
			if u := UserFromContext(r.Context()); u != nil {
				attrs = append(attrs, slog.String("user_id", u.ID.String()))
			}
			logger.LogAttrs(r.Context(), levelForStatus(rec.status), "http_request", attrs...)
		})
	}
}

// statusRecorder wraps an http.ResponseWriter to capture the eventual
// status code. We need this because http.ResponseWriter doesn't expose
// the status after WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		// Mirror net/http's implicit WriteHeader-on-Write behavior so we
		// record status=200 for handlers that skip an explicit call.
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(b)
}

// Hijack lets the WS upgrade flow through (chi's middleware chain wraps
// the writer; our recorder needs to forward Hijack support or the
// upgrade fails).
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("middleware.Logger: ResponseWriter does not support Hijack")
	}
	return hj.Hijack()
}

// Flush forwards to the wrapped writer if it supports flushing — useful
// for SSE-style streaming endpoints (none today, but future-proofing).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func levelForStatus(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
