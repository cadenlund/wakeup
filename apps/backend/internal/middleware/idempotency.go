package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	idemrepo "github.com/cadenlund/wakeup/apps/backend/internal/repository/idempotency"
)

// IdempotencyKeyHeader is the §4.8 header name. Clients send a
// UUID-v7 here on POST/PATCH/PUT to make the request safely retryable.
const IdempotencyKeyHeader = "Idempotency-Key"

// IdempotentReplayHeader is set on every response that went through
// the idempotency middleware. Values:
//
//   - "true"   : a previously-cached (status, body) was replayed
//   - "false"  : the request was processed for the first time and cached
//   - "skipped": body too large, idempotency was bypassed
//
// Absent header means the request had no Idempotency-Key — clients can
// rely on this triplet to know exactly what happened.
const IdempotentReplayHeader = "Idempotent-Replay"

// MaxIdempotentBodyBytes is the §4.8 cap. Requests larger than this skip
// idempotency entirely (with the "skipped" header) so we don't pin huge
// payloads in the database for 24h.
const MaxIdempotentBodyBytes = 256 * 1024

// IdempotencyTTL is the cached-response lifetime. Long enough for any
// realistic client retry strategy.
const IdempotencyTTL = 24 * time.Hour

// IdempotencyStore is the slice of the §3.4 idempotency repository the
// middleware needs. Defining the interface here lets tests stub without
// pulling in pgxpool, and keeps the middleware package free of a
// repository import beyond the type alias for Entry.
//
// The middleware uses Reserve / Complete (the atomic at-most-once
// primitive) for the cache-miss path; Get is still used to read an
// already-completed entry and decide replay vs hash-mismatch. DeleteByKey
// clears the in-flight reservation when the handler produces a 5xx
// (which §4.8 says we don't cache, so client retry shouldn't be blocked
// by a stale placeholder).
type IdempotencyStore interface {
	Get(ctx context.Context, key string, userID uuid.UUID) (idemrepo.Entry, error)
	Reserve(ctx context.Context, p idemrepo.ReserveParams) (idemrepo.Entry, bool, error)
	Complete(ctx context.Context, p idemrepo.CompleteParams) (int64, error)
	DeleteByKey(ctx context.Context, key string, userID uuid.UUID) (int64, error)
}

// IdempotencyConfig packages the dependencies. Logger defaults to
// slog.Default() when nil; Now defaults to time.Now (tests inject a
// fake clock to assert TTL math).
type IdempotencyConfig struct {
	Store      IdempotencyStore
	WriteError errorWriter
	Logger     *slog.Logger
}

// Idempotency wraps POST / PATCH / PUT routes so a client retrying the
// same request with the same Idempotency-Key header gets the same
// response back without the handler being re-invoked.
//
// Concurrency: at-most-once handler execution is guaranteed via an
// atomic reservation pattern. Reserve inserts a placeholder row before
// next.ServeHTTP runs; if a concurrent retry beats us to it the second
// request surfaces 422 IDEMPOTENCY_KEY_REUSED rather than re-running
// the handler. Once the winning handler returns, Complete replaces the
// placeholder with the real (status, headers, body); 5xx responses
// drop the placeholder entirely (§4.8 says don't cache 5xx, and a
// stale placeholder would block legitimate retries).
//
// Algorithm (§4.8):
//
//  1. No header → pass through unchanged. Idempotency is opt-in.
//  2. Header present:
//     a. Read the body fully, hash it together with method+path, restore
//     r.Body so the handler still sees it.
//     b. Body > MaxIdempotentBodyBytes → respond "skipped" header and pass
//     through; we don't cache massive payloads.
//     c. Lookup (key, user_id):
//     - Found, hash matches  → replay the cached status+body. Set
//     Idempotent-Replay: true. Skip the handler.
//     - Found, hash differs  → 422 IDEMPOTENCY_KEY_REUSED.
//     - Not found            → invoke the handler with a buffering
//     ResponseWriter; cache the captured (status, body) when status
//     is 2xx or 4xx (5xx are never cached — they're often transient
//     and the client should be able to retry without hitting a
//     stale 500). Stream the captured response to the real writer
//     and set Idempotent-Replay: false.
//
// RequireAuth must run UPSTREAM of this middleware so user_id is in
// context — keys are scoped per-user (§4.8: "Two users may use the same
// key string without collision").
func Idempotency(cfg IdempotencyConfig) func(http.Handler) http.Handler {
	if cfg.Store == nil {
		panic("middleware.Idempotency: nil Store")
	}
	if cfg.WriteError == nil {
		panic("middleware.Idempotency: nil WriteError")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Idempotency only applies to writes. Anything else passes
			// through untouched (and without the response header).
			if !isWriteMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get(IdempotencyKeyHeader)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			user := UserFromContext(r.Context())
			if user == nil {
				// RequireAuth would have already produced the 401, but
				// in case wiring drifts, surface it explicitly here so
				// idempotency doesn't quietly skip a request that was
				// supposed to be authenticated.
				cfg.WriteError(w, r, apierror.Unauthorized("not authenticated"))
				return
			}

			// Read up to MaxIdempotentBodyBytes+1 — just enough to
			// know whether the body fits the cache. io.ReadAll on the
			// raw Body would buffer arbitrarily large payloads in this
			// middleware, defeating any upstream MaxBytesReader caps
			// (CodeRabbit caught this on PR #74).
			limited, err := io.ReadAll(io.LimitReader(r.Body, MaxIdempotentBodyBytes+1))
			if err != nil {
				cfg.WriteError(w, r, apierror.BadRequest("read body").WithCause(err))
				return
			}
			// Body too large → opt out and tag the response so the
			// client knows idempotency wasn't applied. Re-attach the
			// remaining bytes via MultiReader so the handler still sees
			// the COMPLETE payload (truncating it would silently corrupt
			// the request).
			if len(limited) > MaxIdempotentBodyBytes {
				rest := r.Body
				r.Body = struct {
					io.Reader
					io.Closer
				}{
					Reader: io.MultiReader(bytes.NewReader(limited), rest),
					Closer: rest,
				}
				w.Header().Set(IdempotentReplayHeader, "skipped")
				next.ServeHTTP(w, r)
				return
			}
			body := limited
			// Cache path: body fits, handler will read from a buffered copy.
			r.Body = io.NopCloser(bytes.NewReader(body))

			hash := requestHash(r.Method, r.URL.Path, body)
			// Atomic reservation: try to claim (key, user_id) by
			// inserting a placeholder row. The placeholder's
			// ResponseStatus = idemrepo.PlaceholderStatus until Complete
			// fills in the real response; Reserve returns ok=false with
			// the existing row if another request already claimed.
			//
			// This is the at-most-once primitive that prevents two
			// concurrent retries of the same key from both running the
			// handler.
			entry, reserved, reserveErr := cfg.Store.Reserve(r.Context(), idemrepo.ReserveParams{
				Key: key, UserID: user.ID, RequestHash: hash, TTL: IdempotencyTTL,
			})
			if reserveErr != nil {
				cfg.WriteError(w, r, apierror.Internal("idempotency reserve").WithCause(reserveErr))
				return
			}
			if !reserved {
				// Existing row. Three cases:
				//   1. Real cached response, hash matches → replay.
				//   2. Real cached response, hash differs → 422 reused.
				//   3. Placeholder (in-flight by another request) →
				//      surface as 422 IDEMPOTENCY_KEY_REUSED. Treating it
				//      as a hash mismatch is the safest signal: clients
				//      should retry once the in-flight request settles
				//      (TTL is 24h; the placeholder will be replaced the
				//      moment Complete runs in the other goroutine).
				if entry.ResponseStatus == idemrepo.PlaceholderStatus {
					logger.InfoContext(r.Context(), "idempotency: key in flight on another request",
						slog.String("key", key),
					)
					cfg.WriteError(w, r, apierror.IdempotencyKeyReused())
					return
				}
				if !bytes.Equal(entry.RequestHash, hash) {
					cfg.WriteError(w, r, apierror.IdempotencyKeyReused())
					return
				}
				replayCached(r.Context(), w, entry, logger)
				return
			}
			// We own the reservation. Run the handler exactly once.
			rec := &captureWriter{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			// Mark every fresh request even when we don't cache (5xx
			// case below) so client tooling can distinguish first-time
			// vs replayed responses.
			rec.Header().Set(IdempotentReplayHeader, "false")
			rec.flushHeader()
			if _, writeErr := w.Write(rec.body.Bytes()); writeErr != nil {
				logger.WarnContext(r.Context(), "idempotency: stream response",
					slog.String("error", writeErr.Error()),
				)
				return
			}

			// 5xx responses are not cached — clients should be able to
			// retry without hitting a stale 500 (§4.8). Drop the
			// placeholder so the next attempt isn't blocked by an
			// in-flight reservation.
			if rec.status >= 500 {
				if _, delErr := cfg.Store.DeleteByKey(r.Context(), key, user.ID); delErr != nil {
					logger.WarnContext(r.Context(), "idempotency: clear placeholder on 5xx",
						slog.String("error", delErr.Error()),
					)
				}
				return
			}
			// Replace the placeholder with the real response. Headers
			// snapshot strips Idempotent-Replay (replay sets that
			// itself); body coerced from nil→empty so the schema's
			// NOT NULL response_body is satisfied.
			snapshot := snapshotHeaders(rec.Header())
			cachedBody := rec.body.Bytes()
			if cachedBody == nil {
				cachedBody = []byte{}
			}
			if rows, completeErr := cfg.Store.Complete(r.Context(), idemrepo.CompleteParams{
				Key: key, UserID: user.ID,
				ResponseStatus: rec.status, ResponseHeaders: snapshot,
				ResponseBody: cachedBody, TTL: IdempotencyTTL,
			}); completeErr != nil {
				logger.WarnContext(r.Context(), "idempotency: complete",
					slog.String("error", completeErr.Error()),
				)
			} else if rows == 0 {
				logger.InfoContext(r.Context(), "idempotency: placeholder vanished before complete",
					slog.String("key", key),
				)
			}
		})
	}
}

// replayCached streams a cached entry to w. Pulled out of the main flow
// so the call site stays readable; also a single place to evolve the
// replay format (e.g. Idempotent-Replay-Age in a future milestone).
func replayCached(ctx context.Context, w http.ResponseWriter, entry idemrepo.Entry, logger *slog.Logger) {
	for k, vs := range entry.ResponseHeaders {
		// Idempotent-Replay is set by the middleware itself, not the
		// handler — drop it from the snapshot if any past row carried
		// it (rows written before this commit might have).
		if k == IdempotentReplayHeader {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set(IdempotentReplayHeader, "true")
	w.WriteHeader(entry.ResponseStatus)
	if _, writeErr := w.Write(entry.ResponseBody); writeErr != nil {
		logger.WarnContext(ctx, "idempotency: write cached response",
			slog.String("error", writeErr.Error()),
		)
	}
}

// snapshotHeaders returns a deep-ish copy of h with the Idempotent-Replay
// header stripped — the middleware owns that header, never the handler.
// Multi-value headers (Set-Cookie, etc.) round-trip via the slice copy.
func snapshotHeaders(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if k == IdempotentReplayHeader {
			continue
		}
		copyVs := make([]string, len(vs))
		copy(copyVs, vs)
		out[k] = copyVs
	}
	return out
}

// isWriteMethod reports whether m is one of the §4.8 write verbs.
func isWriteMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPatch, http.MethodPut:
		return true
	}
	return false
}

// requestHash computes the §4.8 SHA-256 over `method " " path "\n" body`.
func requestHash(method, path string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte(" "))
	h.Write([]byte(path))
	h.Write([]byte("\n"))
	h.Write(body)
	return h.Sum(nil)
}

// captureWriter buffers the handler's response so we can persist it to
// the idempotency cache before replaying it on the wire. Implements
// the smallest http.ResponseWriter subset our handlers actually use
// (Header, Write, WriteHeader). Other interfaces (Flusher, Hijacker)
// are intentionally NOT proxied — write endpoints don't need them, and
// re-implementing them with capture semantics is a rabbit hole.
//
// Header() returns a PRIVATE map rather than the underlying writer's
// map. If we returned the live map, we'd snapshot upstream-set headers
// (X-Request-ID, CORS, security headers) on Put, then re-emit them on
// replay where the upstream chain would set them again — duplicating
// or corrupting per-request metadata (CodeRabbit caught this on PR #74).
type captureWriter struct {
	http.ResponseWriter
	headers       http.Header
	body          bytes.Buffer
	status        int
	headerWritten bool
	flushed       bool
}

func (c *captureWriter) Header() http.Header {
	if c.headers == nil {
		c.headers = make(http.Header)
	}
	return c.headers
}

func (c *captureWriter) WriteHeader(status int) {
	if c.headerWritten {
		return
	}
	c.status = status
	c.headerWritten = true
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if !c.headerWritten {
		c.WriteHeader(http.StatusOK)
	}
	return c.body.Write(p)
}

// flushHeader writes the captured headers + status to the underlying
// writer once. Idempotent — a second call is a no-op so the
// explicit-flush path and the implicit-via-Write path don't double up.
func (c *captureWriter) flushHeader() {
	if c.flushed {
		return
	}
	c.flushed = true
	if c.status == 0 {
		c.status = http.StatusOK
	}
	dst := c.ResponseWriter.Header()
	for k, vs := range c.headers {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	c.ResponseWriter.WriteHeader(c.status)
}
