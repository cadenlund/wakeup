package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
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
type IdempotencyStore interface {
	Get(ctx context.Context, key string, userID uuid.UUID) (idemrepo.Entry, error)
	Put(ctx context.Context, p idemrepo.PutParams) (idemrepo.Entry, error)
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
// Concurrency caveat: this implementation matches the §4.8 algorithm
// literally — Get-then-Put-on-miss with ErrConflict on the way out.
// Two truly concurrent requests with the same (user_id, key) can both
// see Get-miss and both run the handler before one Put loses; the
// loser's cache write fails but its handler's side effects already
// landed. For at-most-once *handler execution* (vs at-most-once
// *response delivery*, which we do guarantee for retries) we'd need
// an atomic reservation flow — see PR #74 review thread for the
// followup design.
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
			entry, getErr := cfg.Store.Get(r.Context(), key, user.ID)
			switch {
			case getErr == nil:
				if !bytes.Equal(entry.RequestHash, hash) {
					cfg.WriteError(w, r, apierror.IdempotencyKeyReused())
					return
				}
				replayCached(r.Context(), w, entry, logger)
				return

			case errors.Is(getErr, idemrepo.ErrNotFound):
				// First-time request — fall through to invoke + cache.

			default:
				cfg.WriteError(w, r, apierror.Internal("idempotency lookup").WithCause(getErr))
				return
			}

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

			// Only cache 2xx and 4xx responses. 5xx are often transient
			// — caching them would deny the client a retry. (§4.8 note.)
			if rec.status >= 500 {
				return
			}
			// Copy the captured headers map so a future caller mutation
			// of rec doesn't bleed into the cached entry. Strip the
			// Idempotent-Replay header — replay sets that itself based
			// on whether it was a fresh hit or a cache replay.
			snapshot := snapshotHeaders(rec.Header())
			// bytes.Buffer.Bytes() returns nil when nothing was written;
			// the schema's response_body is NOT NULL, so coerce empty
			// payloads to []byte{} so handlers that produce a status-only
			// response (e.g. 204 No Content) still cache cleanly.
			cachedBody := rec.body.Bytes()
			if cachedBody == nil {
				cachedBody = []byte{}
			}
			if _, putErr := cfg.Store.Put(r.Context(), idemrepo.PutParams{
				Key: key, UserID: user.ID, RequestHash: hash,
				ResponseStatus: rec.status, ResponseHeaders: snapshot,
				ResponseBody: cachedBody,
				TTL:          IdempotencyTTL,
			}); putErr != nil {
				if errors.Is(putErr, idemrepo.ErrConflict) {
					// Concurrent insert: another request with the same
					// (key, user_id) wrote first. Their entry is now
					// authoritative — we don't overwrite it with ours,
					// and we don't fail the current request because the
					// handler already produced a real response on the
					// wire. Log so an unexpectedly hot key surfaces.
					logger.InfoContext(r.Context(), "idempotency: concurrent insert raced",
						slog.String("key", key),
					)
					return
				}
				// Other cache failures are logged but never fail the
				// request — the handler already produced a real response.
				logger.WarnContext(r.Context(), "idempotency: put",
					slog.String("error", putErr.Error()),
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
