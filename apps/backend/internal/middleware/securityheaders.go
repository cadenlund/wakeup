package middleware

import "net/http"

// SecurityHeaders applies the §8.5 baseline. HSTS is opt-in via prod=true
// because httptest's self-signed TLS triggers strict-transport-security
// preloads in dev browsers and that's a footgun.
func SecurityHeaders(prod bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if prod {
				// 1 year, include subdomains, allow preload.
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
			}
			next.ServeHTTP(w, r)
		})
	}
}
