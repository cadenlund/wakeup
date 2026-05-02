package middleware

import (
	"net/http"

	"github.com/google/uuid"
)

// RequestIDHeader is the §4.6 header name. Read on incoming requests
// (so an upstream load balancer's id round-trips), regenerated as a
// UUID v7 when missing, and echoed on the response.
const RequestIDHeader = "X-Request-ID"

// RequestID reads or generates X-Request-ID, attaches it to the request
// context (see RequestIDFromContext), and echoes it on the response.
//
// Generation uses UUID v7 so ids sort by creation time — handy in slog
// streams where request_id makes a natural key.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			if v, err := uuid.NewV7(); err == nil {
				id = v.String()
			}
		}
		if id != "" {
			w.Header().Set(RequestIDHeader, id)
			r = r.WithContext(WithRequestID(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}
