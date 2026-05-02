# Wakeup — Backend Build Specification

> **AI: Read this entire document before writing a single line of code. This is your sole source of truth. Do not deviate. Do not improvise. Do not skip phases. Each checkbox is a contract — check it off only when the work is real, the tests pass, and CI is green.**

---

## 0. Rules of engagement (read first, every time)

These rules are non-negotiable. If a step requires a question, ask the human operator — do not invent answers.

1. **Work strictly top-to-bottom through §15 (the checklist).** No phase begins until the prior phase is fully checked off, committed, CI-green, and CodeRabbit feedback resolved.
2. **One commit per checked-off milestone.** Use the commit message specified for that milestone. Conventional Commits format.
No claude co author should be cadenlund my normal github
   
3. **CI must be green before moving on.** If lint, test, or build fails, stop and fix before the next milestone. Never `--no-verify` past a hook. Never skip tests.
4. **CodeRabbit feedback is binding.** When CodeRabbit comments on a PR or commit, address every actionable comment. Push a follow-up commit (`fix(scope): address CodeRabbit on <thing>`) and wait for CodeRabbit to re-review. Loop until clean.
5. **Tests-first discipline per layer.** Repository → tests → service → tests → handler → tests → swagger annotations → manual smoke via Swagger UI. Never write a service without a tested repository under it.
6. **`t.Parallel()` on every test.** Tests must pass `go test -race -count=10 ./...` cleanly. Use `pgtestdb` for per-test database isolation.
7. **No new dependencies without justification.** Stack is locked in §3. If you genuinely need something not listed, stop and ask.
8. **If you are unsure how a library, API, tool, or protocol works — query the web for the official docs before writing code.** Your training data may be stale. Use WebFetch / WebSearch on the official documentation, GitHub README, or pkg.go.dev. Never invent function signatures, config fields, or HTTP shapes from memory. Cite the source URL in the commit body when the answer was non-obvious.
9. **During the build phase (before first prod deploy), migrations may be edited freely** — keep the schema clean by editing existing files rather than piling on `ALTER TABLE` migrations. After the first production deploy (post-§17 done criteria), migrations become forward-only: any further change is a new file. The single rule that always applies: every migration's `-- +goose Up` block has a matching `-- +goose Down` block that exactly reverses it. Migrations are managed with **goose** (`github.com/pressly/goose/v3`); we use goose's single-file SQL format (one `NNNN_name.sql` file per migration with both Up and Down blocks inline).
10. **Every endpoint gets `swaggo` annotations the same commit it ships.** A handler without annotations is a broken handler. CI enforces this.
11. **STOP and contact Caden if the implementation path is ambiguous or convoluted.** Specifically, do NOT continue silently when:
    - The spec is genuinely silent on a behavior (e.g., "what happens when a user blocks themselves").
    - Two or more viable architectural choices exist with non-obvious trade-offs (e.g., "should I cache this in Redis or hit Postgres each time").
    - A library offers multiple equally-reasonable APIs and the spec doesn't pick one (e.g., "scs cookie sessions vs scs token sessions for the same flow").
    - You find yourself writing more than ~100 lines of code without a clear test target.
    - You are about to introduce a pattern not described anywhere in this spec (a new error envelope, a new auth flow, a new background-job style).
    - You discover the spec contradicts itself.

    The cost of asking is one round-trip. The cost of guessing wrong is rewriting an entire layer. Phrase the question concretely: "Spec §X says A, spec §Y implies B, both reasonable, here are the trade-offs — which?" Wait for an answer. Do NOT continue with both options "until told otherwise."

    Do NOT trigger this rule for: obvious bug fixes, applying a documented pattern to a new aggregate, writing the next test in an established matrix, idiomatic Go choices (slice vs array, etc.).
12. **Final acceptance criterion (§17):** `just verify` is green AND a human can open Swagger UI, log in, and exercise every endpoint with no surprises.

---

## 1. Product overview

**Wakeup** is a friend-graph chat app. No "servers" or guilds (unlike Discord) — just direct relationships and conversations.

**Mobile tabs:** Friends · Conversations · Profile.

**Conversations:** 1:1 or group (max 25 members). Text + a persistent voice/video room per conversation (Discord-style). Default audio-only on join; any participant can enable their camera mid-call. Powered by **self-hosted LiveKit** (no LiveKit Cloud; runs in our docker-compose).

**Defining UX feature:** intelligent presence indicator with a `sleeping` status. (Sleep cycle features deferred to v2 — for v1, "sleeping" is just a manual status.)

**Out of scope for v1:** server/guild model, threads, screen sharing, E2E encryption, sleep tracking, alarms, video calls.

---

## 2. Monorepo layout

```
wakeup/
├── apps/
│   ├── backend/                  # Go API server (this spec)
│   │   ├── cmd/
│   │   │   └── server/
│   │   │       └── main.go       # entry point
│   │   └── internal/
│   │       ├── domain/           # shared aggregate models (User, Conversation, Message, ...) returned by repositories, consumed by services, converted to DTOs by handlers. See §4.11.
│   │       ├── job/              # background-job runner shared by sweepers (§4.12)
│   │       ├── storage/          # pgx pool, DBTX interface, migrations runner
│   │       ├── config/           # koanf-based typed config
│   │       ├── log/              # slog setup
│   │       ├── argon2id/         # wrapper around alexedwards/argon2id
│   │       ├── session/          # wrapper around alexedwards/scs
│   │       ├── apierror/         # typed errors → HTTP response helpers
│   │       ├── pagination/       # cursor pagination helpers
│   │       ├── ratelimit/        # Redis token-bucket limiter
│   │       ├── pubsub/           # Redis pub/sub abstraction for WS fan-out
│   │       ├── mailer/           # Resend wrapper
│   │       ├── pushnotif/        # Expo Push wrapper
│   │       ├── objectstore/      # S3/MinIO abstraction
│   │       ├── wsproto/          # WebSocket message envelope + types
│   │       ├── repository/       # per-aggregate data access
│   │       │   ├── user/
│   │       │   ├── friendship/
│   │       │   ├── conversation/
│   │       │   ├── message/
│   │       │   ├── attachment/
│   │       │   ├── presence/
│   │       │   ├── devicetoken/
│   │       │   └── audit/
│   │       ├── service/          # business logic
│   │       │   ├── auth/
│   │       │   ├── user/
│   │       │   ├── friend/
│   │       │   ├── conversation/
│   │       │   ├── message/
│   │       │   ├── attachment/
│   │       │   ├── presence/
│   │       │   ├── call/
│   │       │   ├── notification/
│   │       │   └── admin/
│   │       ├── handler/          # HTTP + WS handlers
│   │       │   ├── http/
│   │       │   └── ws/
│   │       ├── middleware/
│   │       └── testutil/         # testcontainers helpers, pgtestdb setup
│   └── mobile/                   # Expo (built later, not in this spec)
├── migrations/                   # goose single-file SQL migrations (NNNN_name.sql)
├── docs/
│   └── architecture.md           # generated from this spec, kept current
├── scripts/
│   └── dev/                      # one-off dev scripts
├── docker-compose.yml            # postgres + minio + redis for local dev
├── .github/
│   └── workflows/
│       ├── ci.yml                # lint + test + build
│       └── coderabbit.yml        # CodeRabbit config trigger
├── .coderabbit.yaml
├── .conform.yaml
├── .golangci.yml
├── lefthook.yml
├── justfile
├── .env.example
└── README.md
```

---

## 3. Tech stack (locked)

| Concern | Library / Tool | Why |
|---|---|---|
| Language | Go 1.23+ | — |
| HTTP router | `github.com/go-chi/chi/v5` | idiomatic, middleware-friendly |
| Postgres driver | `github.com/jackc/pgx/v5` (+ `pgxpool`) | best-in-class, native types |
| Migrations | `github.com/pressly/goose/v3` | single-file SQL with `-- +goose Up` / `-- +goose Down` blocks |
| **Sessions** | `github.com/alexedwards/scs/v2` + `scs/pgxstore` | **mandatory — Alex Edwards SCS** |
| **Password hashing** | `github.com/alexedwards/argon2id` | **mandatory — Alex Edwards argon2id** |
| WebSocket | `nhooyr.io/websocket` | actively maintained, context-aware |
| Validation | `github.com/go-playground/validator/v10` | de facto standard |
| Config | `github.com/knadh/koanf/v2` | env + file, no globals |
| Logging | `log/slog` (stdlib) with JSON handler | no extra dep |
| Object storage | `github.com/aws/aws-sdk-go-v2` (S3 client, points at MinIO locally) | one client, two endpoints |
| Voice + video | **Self-hosted LiveKit** (`livekit/livekit-server` Docker image + `github.com/livekit/server-sdk-go/v2` for token issuance + `github.com/livekit/protocol` for webhook decoding) | SFU for 1:1 + group voice/video. Runs in our docker-compose, NOT LiveKit Cloud. |
| Email | Resend (`github.com/resend/resend-go/v2`) | password reset only |
| Push notifications | Expo Push API (HTTP, no SDK needed) | mobile delivery |
| Redis client | `github.com/redis/go-redis/v9` | rate limiting + pub/sub |
| Swagger | `github.com/swaggo/swag` + `github.com/swaggo/http-swagger/v2` | annotations → OpenAPI 3 |
| Client gen | `github.com/oapi-codegen/oapi-codegen/v2` | generates mobile client from OpenAPI |
| Testing | stdlib + `github.com/testcontainers/testcontainers-go` + `github.com/peterldowns/pgtestdb` | template-DB → parallel tests |
| Error tracking | `github.com/getsentry/sentry-go` | day-one observability |
| Lint | `golangci-lint` (config copied from court-scraper) | — |
| Pre-commit | `lefthook` (Go binary) | matches court-scraper |
| Commit lint | `conform` (Go binary, NOT Node commitlint) | matches court-scraper |
| Task runner | `just` | matches court-scraper |
| AI code review | CodeRabbit | mandatory PR review |

**Forbidden:** ORMs (GORM, ent, sqlboiler), JWT for sessions, Node-based dev tooling, anything else not in this table.

---

## 4. Architecture patterns

### 4.1 Layered architecture (one-way deps)

```
handler  →  service  →  repository  →  storage (DBTX)
                     ↘  utility packages (argon2id, session, apierror, ...)
                          ↘  foundational packages (storage, config, log)
```

Everything lives under `apps/backend/internal/`. Layers are tiers, enforced by import discipline:

- **Foundational** — `storage`, `config`, `log`, `domain`. May be imported by anything.
- **Utility** — `argon2id`, `session`, `apierror`, `pagination`, `ratelimit`, `pubsub`, `mailer`, `pushnotif`, `objectstore`, `wsproto`. May import foundational + stdlib only. **Never** import `repository/`, `service/`, `handler/`, `middleware/`.
- **Repository** — data access only. One aggregate per package. May import foundational + utility. Returns domain types, never raw `pgx` rows. Never imports another `repository/`.
- **Service** — business logic. Depends on repository *interfaces* (and concrete repos at wiring time). Never imports `pgx` directly. May depend on multiple repositories.
- **Handler** — parses HTTP/WS, calls service, renders response via `apierror` helpers. Zero business logic.
- **Middleware** — cross-cutting concerns (auth, request-id, rate limit, recovery, logging). May depend on utility + service for context attachment, but never on repository directly.

### 4.2 The DBTX interface (mandatory)

This is the single most important pattern in the codebase. Every repository takes a `DBTX`, not a concrete pool. Both `*pgxpool.Pool` and `pgx.Tx` satisfy it, so repositories work transparently inside or outside transactions.

```go
// apps/backend/internal/storage/dbtx.go
package storage

import (
    "context"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgconn"
)

// DBTX is the interface implemented by both *pgxpool.Pool and pgx.Tx.
// Every repository depends on this — never on a concrete pool.
type DBTX interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}
```

Every repo follows this shape:

```go
type Queries struct {
    db storage.DBTX
}

func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to the given transaction.
// Use this when a service method spans multiple repo calls atomically.
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }
```

A service that needs a transaction does this:

```go
func (s *MessageService) SendWithAttachments(ctx context.Context, ...) error {
    return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
        msgs := s.messages.WithTx(tx)
        atts := s.attachments.WithTx(tx)
        // ... use msgs and atts atomically
        return nil
    })
}
```

### 4.3 SQL convention (sqlc-style by hand)

We do **not** use sqlc. We replicate its discipline manually. Each repository package contains:

```
internal/repository/user/
├── queries.sql      # named queries with sqlc-style header comments
├── models.go        # input/output structs
├── repo.go          # Queries struct + methods
└── repo_test.go     # against testcontainers postgres
```

**`queries.sql`** — named queries, kept in sync with `repo.go` constants:

```sql
-- name: GetUserByID :one
SELECT id, username, display_name, email, password_hash, avatar_url, role, created_at, updated_at
FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: CreateUser :one
INSERT INTO users (id, username, display_name, email, password_hash)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, username, display_name, email, password_hash, avatar_url, role, created_at, updated_at;
```

**`models.go`** — input parameter types only (the *output* type is `domain.User`, lives in `internal/domain/`, see §4.11):

```go
package user

type CreateParams struct {
    ID           uuid.UUID
    Username     string
    DisplayName  string
    Email        string
    PasswordHash string
}

type UpdateParams struct {
    ID          uuid.UUID
    DisplayName *string
    AvatarURL   *string
    ColorScheme *string
}
```

**`repo.go`** — query constants mirror `queries.sql` exactly; methods return `domain.User`:

```go
const getByID = `-- name: GetByID :one
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE id = $1 AND deleted_at IS NULL`

func (q *Queries) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
    row := q.db.QueryRow(ctx, getByID, id)
    var u domain.User
    err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.PasswordHash, &u.AvatarURL, &u.ColorScheme, &u.Role, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt)
    return u, err
}
```

**Discipline rule:** every change to `queries.sql` must be mirrored in `repo.go` in the same commit. CI grep-checks the `-- name:` headers match.

### 4.4 Error handling and response helpers

Error handling is the single most-touched contract surface for the frontend. Get this right once, then never think about it again.

**`internal/apierror/` defines typed errors:**

```go
package apierror

type Code string

const (
    CodeNotFound                   Code = "RESOURCE_NOT_FOUND"
    CodeUnauthorized               Code = "UNAUTHORIZED"
    CodeForbidden                  Code = "FORBIDDEN"
    CodeValidation                 Code = "VALIDATION_FAILED"
    CodeConflict                   Code = "CONFLICT"
    CodeRateLimited                Code = "RATE_LIMITED"
    CodeBadRequest                 Code = "BAD_REQUEST"
    CodePayloadTooLarge            Code = "PAYLOAD_TOO_LARGE"
    CodeIdempotencyKeyReused       Code = "IDEMPOTENCY_KEY_REUSED"
    CodeBlockedDuringImpersonation Code = "BLOCKED_DURING_IMPERSONATION"
    CodeInternal                   Code = "INTERNAL"
)

type FieldError struct {
    Field   string `json:"field"`            // dot path, e.g. "user.email"
    Code    string `json:"code"`             // machine code, e.g. "INVALID_FORMAT", "TOO_SHORT"
    Message string `json:"message"`          // human fallback
}

type Error struct {
    Code              Code         `json:"code"`
    Message           string       `json:"message"`
    Fields            []FieldError `json:"fields,omitempty"`              // populated for VALIDATION_FAILED
    RetryAfterSeconds int          `json:"retry_after_seconds,omitempty"` // populated for RATE_LIMITED
    cause             error        // not serialized — for slog/Sentry
}

func (e *Error) Error() string              { ... }
func (e *Error) Unwrap() error              { return e.cause }
func (e *Error) HTTPStatus() int            { ... }   // table-driven map from Code → status
func (e *Error) WithCause(err error) *Error { e.cause = err; return e }
```

**Constructors:**

```go
func NotFound(resource string) *Error                         { ... }
func Unauthorized(msg string) *Error                          { ... }
func Forbidden(msg string) *Error                             { ... }
func Validation(fields []FieldError) *Error                   { ... }
func Conflict(msg string) *Error                              { ... }
func RateLimited(retryAfterSec int) *Error                    { ... }
func BadRequest(msg string) *Error                            { ... }
func Internal(msg string) *Error                              { ... }   // generic fallback
```

**Handler-side helpers (in `internal/handler/http/respond.go`):**

```go
package http

// WriteJSON marshals body and writes with the given status.
func WriteJSON(w http.ResponseWriter, status int, body any)

// WriteError translates any error into the standard envelope.
//   - *apierror.Error → use its Code, Message, Fields, HTTPStatus
//   - validator.ValidationErrors → convert to apierror.Validation with FieldErrors
//   - any other error → log via slog + Sentry, return CodeInternal with generic message
// Never leaks an unwrapped Go error string to the client.
func WriteError(w http.ResponseWriter, r *http.Request, err error)

// DecodeJSON reads + validates the request body into dst.
// On parse error → BadRequest. On validator failure → Validation.
// Returns the *apierror.Error to write (or nil on success).
func DecodeJSON(r *http.Request, dst any) *apierror.Error
```

**Standard response envelopes the frontend can rely on:**

Single resource (status 200):
```json
{ "id": "...", "username": "caden", "display_name": "Caden" }
```

List with pagination (status 200):
```json
{
  "data": [ { ... }, { ... } ],
  "next_cursor": "eyJpZCI6...",
  "has_more": true
}
```

Normal error (any non-2xx):
```json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "user not found"
  }
}
```

Validation error (status 422):
```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "request validation failed",
    "fields": [
      { "field": "email", "code": "INVALID_FORMAT", "message": "must be a valid email" },
      { "field": "password", "code": "TOO_SHORT", "message": "must be at least 8 characters" }
    ]
  }
}
```

Rate limit error (status 429, plus `Retry-After` header):
```json
{
  "error": {
    "code": "RATE_LIMITED",
    "message": "too many requests",
    "retry_after_seconds": 30
  }
}
```

**Mapping table (Code → HTTP status):**

| Code | HTTP |
|---|---|
| `BAD_REQUEST` | 400 |
| `UNAUTHORIZED` | 401 |
| `FORBIDDEN` | 403 |
| `BLOCKED_DURING_IMPERSONATION` | 403 |
| `RESOURCE_NOT_FOUND` | 404 |
| `CONFLICT` | 409 |
| `PAYLOAD_TOO_LARGE` | 413 |
| `VALIDATION_FAILED` | 422 |
| `IDEMPOTENCY_KEY_REUSED` | 422 |
| `RATE_LIMITED` | 429 |
| `INTERNAL` | 500 |

**Discipline rules:**
- Services return `*apierror.Error`. Never bare `errors.New` or `fmt.Errorf` to a handler.
- Handlers call `WriteError(w, r, err)` exactly once and return.
- Internal errors (unexpected `pgx` failures, etc.) are wrapped via `Internal("...").WithCause(err)`. The cause is logged + sent to Sentry; the original message is **never** leaked to the client.
- `validator.ValidationErrors` is the only thing `WriteError` accepts that isn't already `*apierror.Error` — it auto-converts.
- Every error response includes the request ID (see §4.6) in a `X-Request-ID` header so the frontend can quote it when reporting bugs.

### 4.5 Stateless API + Redis pub/sub

The API is fully stateless. Sessions live in Postgres (via `scs/pgxstore`). WebSocket fan-out crosses instances via Redis pub/sub.

**Channels:**
- `user:<id>:events` — events for a single user (presence, friend requests, etc.)
- `conv:<id>:messages` — events for a conversation

When a service produces an event, it publishes to Redis. Each instance subscribes to channels for the users it currently has WS connections for. The hub on each instance fans out to local connections.

`internal/pubsub` exposes:

```go
type Broker interface {
    Publish(ctx context.Context, channel string, payload []byte) error
    Subscribe(ctx context.Context, channels ...string) (<-chan Message, error)
    Unsubscribe(ctx context.Context, channels ...string) error
}
```

Two implementations: `redisBroker` (prod) and `inProcBroker` (tests, single-instance dev).

### 4.6 Conventions (locked, no exceptions)

These conventions exist so the frontend never has to ask. Apply uniformly.

- **IDs:** UUID v7 everywhere. Generated server-side via `github.com/google/uuid` (NewV7). Never expose database sequence integers.
- **Time:** all timestamps stored in Postgres as `timestamptz`, always **UTC**. Serialized to JSON as **RFC 3339** strings (`2026-05-02T09:31:21.810Z`). Never use Unix epoch ints.
- **JSON casing:** `snake_case` for all field names. Configure `json:"snake_case"` tags. Never `camelCase` or `PascalCase` in API JSON.
- **Enums:** lowercase strings (`"online"`, `"away"`, `"sleeping"`). Never integers.
- **Booleans:** prefixed `is_` or `has_` for clarity (`is_admin`, `has_avatar`).
- **Nullability:** explicit. Use Go pointer types (`*string`, `*time.Time`) for nullable DB columns. Marshal as `null`, not `""` or omitted.
- **Pagination cursor:** opaque base64-encoded JSON `{"id": "...", "ts": "..."}`. Frontend treats as a black box. `next_cursor` is `null` when `has_more` is `false`.
- **Request ID:** every request gets `X-Request-ID` (incoming or generated). Echoed in every response header. Logged on every line via `slog`. Sent to Sentry as a tag. Quoted in user-facing error messages where helpful.
- **Locale/i18n:** v1 is English-only. Error `message` strings are in English. Frontend translates using `code` field, not `message`.
- **Soft-delete behavior (locked):** when a user is soft-deleted (`users.deleted_at` set), **NONE of their content is destroyed.** Their messages, friendships, conversation memberships, attachments, and audit log entries all stay intact. The user's row is excluded from `users` list/search and they cannot log in. Other users still see the deleted user's content in conversation history, with the user's DTO collapsed to `{"id": "...", "username": "deleted-user", "display_name": "Deleted User", "avatar_url": null}` (real fields blanked at the converter, ID kept so the frontend can dedupe). Repositories provide `GetByIDIncludingDeleted` for sender/author lookups so message-history rendering keeps working. **No `ON DELETE CASCADE` ever fires from a soft delete** — those FKs only fire on hard deletes, which v1 does not perform on user records.
- **Field limits (locked — wire identical limits into Postgres CHECK constraints, validator tags, and example/docs):**

| Field | Min | Max | Pattern / notes |
|---|---|---|---|
| `username` | 3 | 32 | `^[a-zA-Z0-9_]+$` (alphanumeric + underscore) — stored as `citext`, so `caden` == `CADEN` for uniqueness AND lookup. Display preserves the case the user typed. |
| `email` | 5 | 254 | RFC 5321; stored as `citext` |
| `display_name` | 1 | 64 | any printable unicode |
| `password` | 8 | 128 | any (entropy enforced by length only) |
| `avatar_url` | 0 | 2048 | URL; `null` if unset |
| `color_scheme` | enum | enum | `light`, `dark`, `system` (default `system`) |
| `message.body` | 1 | 10000 | any |
| `conversation.name` (group) | 1 | 80 | any printable unicode; `null` for direct |
| group member count | 2 | 25 | enforced at service layer |
| `attachment.filename` | 1 | 255 | sanitized server-side per §9.2: strip `/`, `\`, NUL, and control chars; reject empty after sanitization. Stored in DB only — never used in S3 keys (§9.1). |
| `attachment` size (bytes) | 1 | 52428800 | 50 MiB cap |
| `avatar` upload size (bytes) | 1 | 5242880 | 5 MiB cap |
| `Idempotency-Key` header | 1 | 255 | UUID v7 recommended; any unique string accepted |
| `cursor` query param | 0 | 1024 | opaque base64 |
| `q` (search) query param | 0 | 200 | trimmed; `\s+` collapsed |

### 4.7 Cross-cutting middleware (mandatory chain)

Wired in `cmd/server/main.go` in this order, outside-in:

1. **Recovery** — catches panics, logs with stack, sends to Sentry, returns `INTERNAL` 500.
2. **RequestID** — reads or generates `X-Request-ID`, attaches to context + response header.
3. **Logger** — structured `slog` line per request: method, path, status, duration_ms, user_id (if authed), request_id.
4. **CORS** — see §8.4.
5. **SecurityHeaders** — see §8.5.
6. **RateLimit** — Redis token bucket, scoped by route group. See §8.3.
7. **SessionLoad** — `scs.LoadAndSave` reads cookie, populates session.
8. **Auth** (route-scoped) — `RequireAuth` for `/v1/*` except `/auth/register|login|password-reset/*`, `/healthz`, `/readyz`, `/openapi.json`, `/docs`, `/webhooks/livekit`. `RequireAdmin` for `/v1/admin/*`. The webhook endpoint is unauthenticated but signature-verified inside the handler (see §10.3).
9. **Idempotency** (route-scoped: POST/PATCH/PUT only, after Auth so user_id is in context) — also skipped for `/webhooks/livekit`. See §4.9.

### 4.8 Idempotency middleware

Every write request (`POST`, `PATCH`, `PUT`) **may** carry an `Idempotency-Key` header — a client-generated UUID v7. If present, the middleware ensures the request is processed at most once.

**Algorithm:**

1. If header absent → pass through to handler. (Idempotency is opt-in.)
2. If header present:
   1. Read the request body fully (buffer it; replace `r.Body` so the handler can still read).
   2. Compute `request_hash = SHA-256(method + " " + path + "\n" + body)`.
   3. Look up `(key, user_id)` in `idempotency_keys`.
      - **Found AND `request_hash` matches stored:** return cached `(response_status, response_body)` to the client; add header `Idempotent-Replay: true`. Do NOT invoke the handler.
      - **Found AND `request_hash` differs:** return 422 `IDEMPOTENCY_KEY_REUSED` with message `"idempotency key already used for a different request"`. Do NOT invoke the handler.
      - **Not found:** invoke the handler with a `httptest.ResponseRecorder`-style buffer; capture status + body; persist `(key, user_id, request_hash, status, body)` with TTL of 24 hours; write the response to the real `ResponseWriter`.
3. A background sweeper goroutine deletes rows where `expires_at < now()` every 1 hour.

**Notes:**
- Keys are scoped per `user_id`. Two users may use the same key string without collision.
- TTL is 24 hours — long enough for any client retry strategy.
- Only successful and 4xx responses are cached. 5xx responses are NOT cached (they may have been transient — the client should be able to retry without hitting a cached 500).
- Capping body size for hash and storage: requests over 256 KB skip idempotency (return a `Idempotent-Replay: skipped` header). This is documented in the Swagger description for write endpoints.

### 4.9 Graceful shutdown

`cmd/server/main.go` listens for `SIGINT` + `SIGTERM`. On signal:

1. Stop accepting new HTTP connections (`server.Shutdown(ctx)` with 30s timeout).
2. Close the WebSocket hub: send close frames to all conns, wait up to 10s for them to flush.
3. Stop the presence sweeper goroutine.
4. Drain the pubsub subscriber.
5. Close the pgx pool.
6. Flush Sentry (`sentry.Flush(2 * time.Second)`).
7. Exit 0.

If the second signal arrives during shutdown, exit 1 immediately.

### 4.10 Handler DTOs (input + output) — never serialize domain types

Every handler defines its own request and response types in the `internal/handler/http/` package. These are **distinct** from domain/repository types in `internal/repository/<aggregate>/`. The handler converts between them.

**Why DTOs are mandatory:**
- Domain types include sensitive fields (`password_hash`) that must NEVER reach the wire. A misplaced `json.Marshal(user)` is a security incident.
- Domain types include internal fields (cursor positions, soft-delete timestamps, internal IDs) the frontend has no business knowing.
- DTOs decouple the API contract from schema. Renaming `messages.body` → `messages.content` doesn't break clients if the DTO field stays `body`.
- DTOs are the natural home for `example:` and Swagger annotations.

**Pattern — every aggregate has a small set of DTOs in `internal/handler/http/<aggregate>_dto.go`:**

```go
// internal/handler/http/user_dto.go

// UserResponse — public profile view. Used in friend lists, message senders,
// conversation members. NEVER includes email, password_hash, role, or notification prefs.
type UserResponse struct {
    ID          uuid.UUID `json:"id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
    Username    string    `json:"username"     example:"caden"`
    DisplayName string    `json:"display_name" example:"Caden Lund"`
    AvatarURL   *string   `json:"avatar_url"   example:"https://wakeup.app/avatars/caden.png"`
    CreatedAt   time.Time `json:"created_at"   example:"2026-05-02T09:31:21.810Z"`
}

// MeResponse — authenticated self view. Includes private fields the user
// is allowed to see about themselves: email, role, color_scheme.
// During admin impersonation (§8.7): this returns the IMPERSONATED user's
// fields, with ImpersonatedBy populated so the UI can render a banner.
type MeResponse struct {
    ID             uuid.UUID         `json:"id"              example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
    Username       string            `json:"username"        example:"caden"`
    DisplayName    string            `json:"display_name"    example:"Caden Lund"`
    Email          string            `json:"email"           example:"caden@example.com"`
    AvatarURL      *string           `json:"avatar_url"      example:"https://wakeup.app/avatars/caden.png"`
    ColorScheme    string            `json:"color_scheme"    example:"system"`
    Role           string            `json:"role"            example:"user"`
    CreatedAt      time.Time         `json:"created_at"      example:"2026-05-02T09:31:21.810Z"`
    ImpersonatedBy *ImpersonatorInfo `json:"impersonated_by,omitempty"`  // populated only during admin impersonation
}

// ImpersonatorInfo identifies the admin currently impersonating this session.
// Frontend uses this to render the "You are impersonating @<user> — End" banner.
type ImpersonatorInfo struct {
    ID          uuid.UUID `json:"id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
    Username    string    `json:"username"     example:"baron"`
    DisplayName string    `json:"display_name" example:"Baron Admin"`
}

// UpdateMeRequest — fields the user can patch on themselves. All optional.
type UpdateMeRequest struct {
    DisplayName *string `json:"display_name,omitempty" validate:"omitempty,min=1,max=64"                    example:"Caden Lund"`
    AvatarURL   *string `json:"avatar_url,omitempty"   validate:"omitempty,url,max=2048"                  example:"https://..."`
    ColorScheme *string `json:"color_scheme,omitempty" validate:"omitempty,oneof=light dark system"        example:"dark"`
}

// Conversion helpers live next to the DTOs in the handler package.
// They are pure functions — no I/O, no errors. Input is domain.User (§4.11).
func toUserResponse(u domain.User) UserResponse {
    // Soft-deleted user → render as "Deleted User" placeholder per §4.6.
    if u.DeletedAt != nil {
        return UserResponse{
            ID: u.ID, Username: "deleted-user", DisplayName: "Deleted User",
            AvatarURL: nil, CreatedAt: u.CreatedAt,
        }
    }
    return UserResponse{
        ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
        AvatarURL: u.AvatarURL, CreatedAt: u.CreatedAt,
    }
}

func toMeResponse(u domain.User) MeResponse {
    return MeResponse{
        ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
        Email: u.Email, AvatarURL: u.AvatarURL, ColorScheme: u.ColorScheme,
        Role: u.Role, CreatedAt: u.CreatedAt,
    }
}
```

**Discipline rules:**
- **Every** endpoint with a request body has a `<Verb><Resource>Request` DTO. No exceptions.
- **Every** endpoint with a response body has a `<Resource>Response` (single) or `<Resource>ListResponse` (list) DTO. No exceptions.
- DTOs live in `internal/handler/http/<aggregate>_dto.go` next to the handler.
- `to<Resource>Response()` functions are pure (no errors). They live in the handler package, never in `service/` or `repository/`. They take `domain.<Resource>` (from `internal/domain/`, see §4.11) and return the DTO.
- Sensitive fields (`password_hash`, internal cursors, raw `deleted_at`) are stripped at the DTO boundary by *omission*, not by zeroing.
- Per-handler test suite includes a `TestX_NoLeak` subtest that JSON-marshals every response and asserts forbidden field names (`password_hash`, etc.) do not appear in the output.

### 4.11 Domain types live in `internal/domain/`

All shared aggregate models (the things repositories return and services consume) live in `internal/domain/`, one file per aggregate:

```
internal/domain/
├── user.go             // type User struct { ... }
├── friendship.go
├── conversation.go
├── message.go
├── attachment.go
├── presence.go
├── notificationpref.go
└── audit.go
```

**Rule:** the *output* type a repository returns is `domain.<X>`. The *input* parameter types (`CreateUserParams`, `UpdateConversationParams`) stay in the repository package's `models.go` — they're per-repo concerns, not shared.

```go
// internal/domain/user.go
package domain

type User struct {
    ID           uuid.UUID
    Username     string
    DisplayName  string
    Email        string
    PasswordHash string  // not exposed via DTO — handler strips
    AvatarURL    *string
    ColorScheme  string
    Role         string
    CreatedAt    time.Time
    UpdatedAt    time.Time
    DeletedAt    *time.Time
}

// internal/repository/user/models.go
package user

type CreateParams struct {
    ID           uuid.UUID
    Username     string
    DisplayName  string
    Email        string
    PasswordHash string
}

// internal/repository/user/repo.go
func (q *Queries) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) { ... }
func (q *Queries) Create(ctx context.Context, p CreateParams) (domain.User, error) { ... }
```

`domain/` may import only stdlib + `github.com/google/uuid`. It must not import `repository/`, `service/`, `handler/`, or any other internal package — it's the leaf shared by the rest.

### 4.12 Background job runner (one pattern, three jobs)

Three goroutines run continuously inside the server: presence sweeper (30s), idempotency-key sweeper (1h), expired-session sweeper (1h). All three use the same runner pattern so lifecycle is uniform.

```go
// internal/job/runner.go
package job

type Job interface {
    Name() string
    Interval() time.Duration
    Run(ctx context.Context) error
}

type Runner struct {
    jobs   []Job
    log    *slog.Logger
    wg     sync.WaitGroup
    cancel context.CancelFunc
}

func New(log *slog.Logger) *Runner

func (r *Runner) Register(j Job)

// Start launches every registered job in its own goroutine. Each job ticks
// at its declared Interval. Job errors are logged + sent to Sentry; the
// goroutine continues. Call exactly once per process.
func (r *Runner) Start(ctx context.Context)

// Stop cancels the context and blocks until every job goroutine has returned.
// Call from main.go shutdown sequence (see §4.9 graceful shutdown).
func (r *Runner) Stop()
```

**Implementations live in their respective service packages:**

```go
// internal/service/presence/sweeper.go
type Sweeper struct{ svc *Service }
func (s *Sweeper) Name() string                     { return "presence-sweeper" }
func (s *Sweeper) Interval() time.Duration          { return 30 * time.Second }
func (s *Sweeper) Run(ctx context.Context) error    { return s.svc.SweepStale(ctx) }
```

(Same shape for `internal/service/idempotency/sweeper.go` returning 1h interval, and `internal/service/auth/session_sweeper.go` returning 1h interval.)

**Wired in `cmd/server/main.go`:**

```go
runner := job.New(logger)
runner.Register(&presence.Sweeper{Svc: presenceSvc})
runner.Register(&idempotency.Sweeper{Svc: idempSvc})
runner.Register(&auth.SessionSweeper{Svc: authSvc})
runner.Start(ctx)
defer runner.Stop()  // blocks during graceful shutdown
```

The runner's `Stop()` is invoked from the shutdown sequence in §4.9, between "stop accepting HTTP" and "close pgx pool."

**Tests:** `runner_test.go` covers: registered job runs at interval, job error is logged + reported to fake Sentry but loop continues, `Stop()` cancels in-flight runs and returns within the test's deadline.

---

## 5. Domain model (full schema)

All UUIDs are v7. Soft-deletable tables have `deleted_at timestamptz NULL`.

### 5.1 Migrations

**Format — goose single-file:** every migration is a single `migrations/NNNN_name.sql` file containing both an `-- +goose Up` block and an `-- +goose Down` block. Wrap any multi-statement block (e.g. `CREATE FUNCTION ... $$ ... $$`, transactional DDL groups) in `-- +goose StatementBegin` / `-- +goose StatementEnd` so goose treats it as one statement. The Down block must exactly reverse the Up block.

The SQL bodies below are the **Up** content for each migration. The Down content is "drop everything created above, in reverse dependency order" — write it explicitly in the same file (`DROP TRIGGER ...`, `DROP INDEX ...`, `DROP TABLE ... CASCADE`, `DROP FUNCTION ...`, `DROP EXTENSION ... IF EXISTS`).

```sql
-- migrations/0001_init.sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS citext;

-- Reusable trigger function for updated_at columns.
-- ATTACH THIS TRIGGER TO EVERY TABLE THAT HAS an updated_at COLUMN.
-- The application MUST NOT set updated_at manually — the trigger owns it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TABLE users (
    id              uuid PRIMARY KEY,
    username        citext NOT NULL UNIQUE,                                          -- citext: 'caden' and 'CADEN' are the same username
    display_name    text NOT NULL,
    email           citext NOT NULL UNIQUE,
    password_hash   text NOT NULL,
    avatar_url      text,
    color_scheme    text NOT NULL DEFAULT 'system' CHECK (color_scheme IN ('light','dark','system')),
    role            text NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE INDEX users_username_trgm_idx ON users USING gin (username gin_trgm_ops);
CREATE INDEX users_display_name_trgm_idx ON users USING gin (display_name gin_trgm_ops);
CREATE INDEX users_active_idx ON users (id) WHERE deleted_at IS NULL;

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS users_set_updated_at ON users;
DROP INDEX IF EXISTS users_active_idx;
DROP INDEX IF EXISTS users_display_name_trgm_idx;
DROP INDEX IF EXISTS users_username_trgm_idx;
DROP TABLE IF EXISTS users;
DROP FUNCTION IF EXISTS set_updated_at();
DROP EXTENSION IF EXISTS citext;
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS pgcrypto;
```

```sql
-- migrations/0002_sessions.sql -- (-- +goose Up ... -- +goose Down ... pattern; see 0001 above)
-- alexedwards/scs pgxstore expected schema (verbatim from package docs)
CREATE TABLE sessions (
    token   TEXT PRIMARY KEY,
    data    BYTEA NOT NULL,
    expiry  TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);
```

```sql
-- migrations/0003_friendships.sql
CREATE TABLE friendships (
    id            uuid PRIMARY KEY,
    requester_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    addressee_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status        text NOT NULL CHECK (status IN ('pending','accepted','blocked')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    accepted_at   timestamptz,
    UNIQUE (requester_id, addressee_id),
    CHECK (requester_id <> addressee_id)
);
CREATE INDEX friendships_addressee_idx ON friendships (addressee_id, status);
CREATE INDEX friendships_requester_idx ON friendships (requester_id, status);
```

```sql
-- migrations/0004_conversations.sql
CREATE TABLE conversations (
    id            uuid PRIMARY KEY,
    type          text NOT NULL CHECK (type IN ('direct','group')),
    name          text,
    avatar_url    text,
    created_by    uuid NOT NULL REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    last_message_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE conversation_members (
    conversation_id        uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id                uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role                   text NOT NULL DEFAULT 'member' CHECK (role IN ('member','admin')),
    joined_at              timestamptz NOT NULL DEFAULT now(),
    last_read_message_id   uuid,
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX conversation_members_user_idx ON conversation_members (user_id);

CREATE TRIGGER conversations_set_updated_at
    BEFORE UPDATE ON conversations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

```sql
-- migrations/0005_messages.sql
CREATE TABLE messages (
    id                   uuid PRIMARY KEY,
    conversation_id      uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_id            uuid NOT NULL REFERENCES users(id),
    body                 text NOT NULL,
    body_tsv             tsvector GENERATED ALWAYS AS (to_tsvector('english', body)) STORED,
    reply_to_message_id  uuid REFERENCES messages(id),
    created_at           timestamptz NOT NULL DEFAULT now(),
    edited_at            timestamptz,
    deleted_at           timestamptz
);
CREATE INDEX messages_conv_created_idx ON messages (conversation_id, created_at DESC);
CREATE INDEX messages_body_tsv_idx ON messages USING gin (body_tsv);

CREATE TABLE message_attachments (
    message_id     uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    attachment_id  uuid NOT NULL,
    PRIMARY KEY (message_id, attachment_id)
);

CREATE TABLE message_reads (
    message_id   uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id)
);
CREATE INDEX message_reads_user_idx ON message_reads (user_id);
```

```sql
-- migrations/0006_attachments.sql
CREATE TABLE attachments (
    id            uuid PRIMARY KEY,
    uploader_id   uuid NOT NULL REFERENCES users(id),
    storage_key   text NOT NULL,
    filename      text NOT NULL,
    content_type  text NOT NULL,
    size_bytes    bigint NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
```

```sql
-- migrations/0007_presence.sql
CREATE TABLE presence_states (
    user_id            uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status             text NOT NULL DEFAULT 'offline' CHECK (status IN ('online','away','offline','sleeping')),
    last_active_at     timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at  timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER presence_states_set_updated_at
    BEFORE UPDATE ON presence_states
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

```sql
-- migrations/0008_password_resets.sql
CREATE TABLE password_resets (
    token_hash   bytea PRIMARY KEY,    -- sha256 of the token sent to email
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz
);
CREATE INDEX password_resets_user_idx ON password_resets (user_id);
```

```sql
-- migrations/0009_device_tokens.sql
CREATE TABLE device_tokens (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expo_token  text NOT NULL,
    platform    text NOT NULL CHECK (platform IN ('ios','android')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, expo_token)
);
```

```sql
-- migrations/0010_audit_log.sql
CREATE TABLE audit_log (
    id          uuid PRIMARY KEY,
    actor_id    uuid REFERENCES users(id),
    action      text NOT NULL,
    target_type text,
    target_id   uuid,
    metadata    jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_id);
```

```sql
-- migrations/0011_idempotency_keys.sql
-- Caches the response body for write requests carrying an Idempotency-Key header.
-- See §4.9 for middleware semantics.
CREATE TABLE idempotency_keys (
    key             text PRIMARY KEY,                                                -- client-supplied UUID v7 (or any unique string ≤ 255 chars)
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,            -- key is scoped per user
    request_hash    bytea NOT NULL,                                                  -- sha256(method + path + body)
    response_status int NOT NULL,
    response_body   bytea NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL DEFAULT (now() + interval '24 hours')
);
CREATE INDEX idempotency_keys_user_idx ON idempotency_keys (user_id);
CREATE INDEX idempotency_keys_expires_idx ON idempotency_keys (expires_at);
```

```sql
-- migrations/0012_notification_preferences.sql
-- Per-user toggles for push notification categories.
-- A row is auto-created with defaults the first time the user is fetched.
-- Push delivery (Phase 11) checks the relevant flag before sending.
CREATE TABLE notification_preferences (
    user_id              uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    direct_messages      boolean NOT NULL DEFAULT true,
    group_messages       boolean NOT NULL DEFAULT true,
    friend_requests      boolean NOT NULL DEFAULT true,
    calls                boolean NOT NULL DEFAULT true,
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER notification_preferences_set_updated_at
    BEFORE UPDATE ON notification_preferences
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

**Schema rules:**
- Every migration is a single `migrations/NNNN_name.sql` file with both `-- +goose Up` and `-- +goose Down` blocks. The Down block must exactly reverse the Up block.
- Wrap any statement that contains internal semicolons (e.g. `CREATE FUNCTION ... $$ ... $$`) in `-- +goose StatementBegin` / `-- +goose StatementEnd` so goose treats it as one statement. See `0001_init.sql` above for the pattern.
- The `set_updated_at()` trigger from `0001_init.sql` MUST be attached to every table that has an `updated_at` column. The application MUST NOT set `updated_at` manually — the trigger owns it.
- During the build phase, edit existing migrations to keep them clean (per §0 rule 9). After first prod deploy, migrations become forward-only.

---

## 6. API surface (v1)

### 6.1 Conventions

- **Versioning:** all paths prefixed `/v1/`. Breaking changes → `/v2/`.
- **Pagination:** every list endpoint uses cursor pagination via `internal/pagination`. Query params: `limit` (default 20, max 100), `cursor` (opaque base64).
- **Search:** list endpoints take `q` for trigram or full-text search where defined.
- **Error envelope:** `{ "error": { "code", "message", "details" } }`.
- **Success envelope:** raw resource for `:one`, `{ "data": [...], "next_cursor": "...", "has_more": bool }` for `:many`.
- **Auth:** session cookie (`SameSite=Lax`, `Secure`, `HttpOnly`), backed by `scs`. Cookies only — no Bearer-token alternative. Mobile (Expo) handles cookies via Expo's session package; the backend is cookie-only and doesn't care which client is sending them.
- **Rate limiting:** Redis token bucket via `internal/ratelimit`. Defaults: 10/min on `/v1/auth/*`, 60/min on writes, 300/min on reads.
- **Idempotency:** all `POST`/`PATCH`/`PUT` endpoints accept an optional `Idempotency-Key: <uuid-v7>` header. See §4.8. Document this in every write endpoint's swaggo annotations as `@Param Idempotency-Key header string false "Idempotency key (UUID v7); enables safe retries"`.
- **Examples are mandatory.** Every request struct field and every response struct field has a `example:"..."` tag. Swagger UI uses these to pre-populate the "Try it out" form so the operator can hit Execute without typing anything. See §6.3 for the full template. CI (via `swag init`) fails if a documented field is missing an example.
- **Content type:** JSON only, except `multipart/form-data` for upload endpoints.

### 6.2 Endpoint inventory

```
auth
  POST   /v1/auth/register                    body: { username, email, display_name, password }
  POST   /v1/auth/login                       body: { identifier, password }   identifier = username|email
  POST   /v1/auth/logout
  POST   /v1/auth/logout-all
  GET    /v1/auth/me
  POST   /v1/auth/password-reset/request      body: { email }                  always 204 (no enumeration)
  POST   /v1/auth/password-reset/confirm      body: { token, new_password }

users
  GET    /v1/users?q=&limit=&cursor=          search by username/display_name (trigram)
  GET    /v1/users/{id}                       UserResponse (public profile)
  PATCH  /v1/users/me                         body: { display_name?, avatar_url?, color_scheme? }
  POST   /v1/users/me/avatar                  multipart, max 5MB, image/* only
  GET    /v1/users/me/notifications           NotificationPreferencesResponse
  PATCH  /v1/users/me/notifications           body: any subset of { direct_messages?, group_messages?, friend_requests?, calls? }
  DELETE /v1/users/me                         soft delete

friends
  GET    /v1/friends?limit=&cursor=           accepted friends + presence
  GET    /v1/friends/requests                 pending in/out
  POST   /v1/friends/requests                 body: { username }
  POST   /v1/friends/requests/{id}/accept
  POST   /v1/friends/requests/{id}/decline
  DELETE /v1/friends/{user_id}                unfriend
  POST   /v1/friends/{user_id}/block
  DELETE /v1/friends/{user_id}/block

conversations
  GET    /v1/conversations?limit=&cursor=     sorted by last_message_at DESC
  POST   /v1/conversations                    body: { type, member_ids[], name? }   group cap 25
  GET    /v1/conversations/{id}
  PATCH  /v1/conversations/{id}               group only: { name?, avatar_url? }
  DELETE /v1/conversations/{id}               leave (group) or hide (direct)
  POST   /v1/conversations/{id}/members       body: { user_ids[] }   admin only
  DELETE /v1/conversations/{id}/members/{user_id}
  POST   /v1/conversations/{id}/read          body: { up_to_message_id }

messages
  GET    /v1/conversations/{id}/messages?limit=&cursor=&q=    reverse chrono; q = full-text
  POST   /v1/conversations/{id}/messages      body: { body, attachment_ids?, reply_to_message_id? }
  PATCH  /v1/messages/{id}                    body: { body }    own only
  DELETE /v1/messages/{id}                    soft delete; own (or conv admin)
  GET    /v1/messages/{id}/reads              who has read this message

attachments
  POST   /v1/attachments                      multipart, max 50MB, server-side MIME detection + whitelist (§9.2)
  GET    /v1/attachments/{id}                 returns AttachmentResponse with presigned URL (5min TTL); membership-gated (§9.3, §9.7)

presence
  GET    /v1/presence/friends                 bulk: status of all my friends (used on app open)
  POST   /v1/presence/status                  body: { status: 'online'|'sleeping' }   manual override

rooms (voice/video — every conversation has one persistent room: room_id == conversation_id)
  POST   /v1/conversations/{id}/room/join     body: { video?: bool=false }   → { room_id, livekit_url, livekit_token, expires_at, video }
  POST   /v1/conversations/{id}/room/leave    body: empty   (best-effort; LiveKit also detects disconnect)
  GET    /v1/conversations/{id}/room          → { participants: [{ user_id, joined_at, video }], started_at }

webhooks (no auth — verified by LiveKit signature)
  POST   /webhooks/livekit                    LiveKit fires room/participant events here

device tokens (push)
  POST   /v1/devices                          body: { expo_token, platform }
  DELETE /v1/devices/{id}

widget (lightweight, polled every ~15min by mobile)
  GET    /v1/widget/friends                   compact array of top N friends + presence

admin (role=admin only)
  GET    /v1/admin/users?q=&limit=&cursor=
  GET    /v1/admin/users/{id}
  PATCH  /v1/admin/users/{id}                 { role?, deleted_at? }
  POST   /v1/admin/users/{id}/impersonate     start impersonating this user → returns their MeResponse with impersonated_by
  POST   /v1/admin/impersonate/end            stop impersonating → returns admin's MeResponse
  GET    /v1/admin/audit?limit=&cursor=

websocket
  GET    /v1/ws                               upgrade; auth via Authorization or cookie

system
  GET    /v1/healthz                          liveness, no auth
  GET    /v1/readyz                           checks db + redis
  GET    /v1/openapi.json                     spec
  GET    /v1/docs                             Swagger UI
```

### 6.3 Swaggo annotation template (mandatory shape — copy this verbatim per endpoint)

Every handler is annotated in the comment block immediately above its function. Every request/response struct has `example:"..."` tags on every field so Swagger UI's "Try it out" pre-fills realistic values and is **executable with zero typing**.

**Full worked example — `POST /v1/auth/register`:**

```go
// Register creates a new user account and returns a session.
//
// @Summary      Register a new user
// @Description  Creates a new user account, hashes the password with argon2id, and returns a session token. The session is also set as an HttpOnly cookie.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        Idempotency-Key  header   string             false  "Idempotency key (UUID v7); enables safe retries"  example("0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c")
// @Param        request          body     RegisterRequest    true   "Registration payload"
// @Success      201              {object} RegisterResponse   "Created"
// @Header       201              {string} X-Request-ID       "Echoed request id"
// @Failure      400              {object} apierror.Error     "Malformed JSON"
// @Failure      409              {object} apierror.Error     "Username or email already taken"
// @Failure      422              {object} apierror.Error     "Validation failed"
// @Failure      429              {object} apierror.Error     "Rate limited"
// @Failure      500              {object} apierror.Error     "Internal error"
// @Router       /v1/auth/register [post]
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) { ... }

// RegisterRequest is the body for POST /v1/auth/register.
type RegisterRequest struct {
    Username    string `json:"username"     validate:"required,min=3,max=32,alphanum"  example:"caden"`
    Email       string `json:"email"        validate:"required,email,max=254"          example:"caden@example.com"`
    DisplayName string `json:"display_name" validate:"required,min=1,max=64"           example:"Caden Lund"`
    Password    string `json:"password"     validate:"required,min=8,max=128"          example:"correct-horse-battery-staple"`
}

// RegisterResponse is returned on successful registration.
type RegisterResponse struct {
    User  User   `json:"user"`
    Token string `json:"token" example:"3f8a1c2d-9e6b-4f2a-8d1c-7b3e9f5a2c4d"`
}

// User is the public user representation. Used in many response types.
type User struct {
    ID          uuid.UUID  `json:"id"           example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
    Username    string     `json:"username"     example:"caden"`
    DisplayName string     `json:"display_name" example:"Caden Lund"`
    AvatarURL   *string    `json:"avatar_url"   example:"https://wakeup.app/avatars/caden.png"`
    Role        string     `json:"role"         example:"user"`
    CreatedAt   time.Time  `json:"created_at"   example:"2026-05-02T09:31:21.810Z"`
}
```

**Rules — apply to every handler:**

- The comment block uses the exact tags shown above. No alternate forms.
- `@Tags` matches the route group (`auth`, `users`, `friends`, `conversations`, `messages`, `attachments`, `presence`, `calls`, `devices`, `widget`, `admin`, `system`).
- Every status code that a `WriteError` could plausibly produce in this handler MUST appear as a `@Failure`. CI (per §13.6) cross-checks against the test matrix in §12.5.
- For write endpoints, the `Idempotency-Key` header `@Param` is mandatory.
- Every field of every request and response struct has both `validate:"..."` (where applicable) and `example:"..."` tags. Examples are realistic — never `"string"`, `"value"`, or `"foo"`.
- For nullable fields (`*string`, `*time.Time`), the example shows the populated value; Swagger renders `null` as the alternative automatically.
- For list responses, use the standard envelope:

```go
type ListMessagesResponse struct {
    Data       []Message `json:"data"`
    NextCursor *string   `json:"next_cursor"  example:"eyJpZCI6ImFiYyIsInRzIjoiMjAyNi0wNS0wMlQwOTozMToyMS44MTBaIn0="`
    HasMore    bool      `json:"has_more"     example:"true"`
}
```

- For multipart uploads, use `@Accept multipart/form-data` and `@Param file formData file true "..."`. Example payloads aren't required for binary fields, but every text part needs an example.

**Tagging the spec itself in `cmd/server/main.go`:**

```go
// @title           Wakeup API
// @version         1.0
// @description     Friend-graph chat backend. See docs/WAKEUP.md for the full spec.
// @host            localhost:8080
// @BasePath        /
// @schemes         http https
//
// @securityDefinitions.apikey CookieAuth
// @in cookie
// @name wakeup_session
package main
```

Apply `@Security CookieAuth` on every authenticated handler so the Swagger UI "Authorize" button gates calls. Cookie-only — no Bearer.

### 6.4 Cursor pagination — keyset SQL pattern (mandatory for every list endpoint)

**Never use `OFFSET` for pagination.** Offset breaks under concurrent inserts (rows shift between pages, you get duplicates and gaps) and gets slow as offset grows. Every list endpoint uses **keyset pagination** instead. The `internal/pagination` package provides the helpers; every repository's `List*` method follows this pattern verbatim.

**The cursor:**

```go
// internal/pagination/cursor.go

// Cursor is the opaque value sent to/from clients as a base64 string.
type Cursor struct {
    Timestamp time.Time `json:"ts"`
    ID        uuid.UUID `json:"id"`
}

// Encode returns the base64 envelope. Empty cursor → empty string.
func Encode(c *Cursor) string

// Decode parses a base64 envelope. Returns (nil, nil) for empty input (= first page).
// Returns apierror.BadRequest("invalid cursor") on malformed input.
func Decode(s string) (*Cursor, error)
```

**The repository pattern — copy this shape for every list method:**

```sql
-- name: ListMessagesByConversation :many
-- Keyset pagination on (created_at DESC, id DESC).
-- $2 and $3 are NULL for the first page; on subsequent pages they're
-- the timestamp + id of the LAST row of the previous page.
SELECT id, conversation_id, sender_id, body, created_at, edited_at
FROM messages
WHERE conversation_id = $1
  AND deleted_at IS NULL
  AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3))
ORDER BY created_at DESC, id DESC
LIMIT $4;
```

```go
// In internal/repository/message/repo.go

func (q *Queries) ListMessagesByConversation(
    ctx context.Context,
    convID uuid.UUID,
    cursor *pagination.Cursor,
    limit int,
) ([]domain.Message, error) {
    var ts *time.Time
    var id *uuid.UUID
    if cursor != nil {
        ts = &cursor.Timestamp
        id = &cursor.ID
    }
    // ALWAYS query limit + 1 — the extra row tells us whether HasMore.
    rows, err := q.db.Query(ctx, listMessagesByConversation, convID, ts, id, limit+1)
    if err != nil { return nil, err }
    defer rows.Close()
    // ... scan into []domain.Message
}
```

**Service layer wraps the repo call into a Page:**

```go
// pagination.Page accepts the over-fetched slice + the requested limit and
// the per-row cursor extractor. Returns trimmed data + next_cursor + has_more.
func Page[T any](rows []T, limit int, getCursor func(T) Cursor) (data []T, next *string, hasMore bool)

// Usage in service:
rows, err := s.repo.ListMessagesByConversation(ctx, convID, cursor, limit)
if err != nil { return nil, err }
data, next, hasMore := pagination.Page(rows, limit, func(m domain.Message) pagination.Cursor {
    return pagination.Cursor{Timestamp: m.CreatedAt, ID: m.ID}
})
return ListMessagesResponse{Data: toMessageResponses(data), NextCursor: next, HasMore: hasMore}, nil
```

**Index requirement:** every keyset query needs a composite index matching the ORDER BY. For messages, the schema already has `CREATE INDEX messages_conv_created_idx ON messages (conversation_id, created_at DESC)` — extend or add per-aggregate indexes as needed when adding new list endpoints.

**Why `(created_at, id)` and not just `created_at`:** two messages can share a `created_at` to the microsecond. The compound key breaks ties deterministically by ID, so pages don't drop or duplicate rows.

**Test cases for every list endpoint (folded into §12.5 matrix):**
- empty result set
- exactly `limit` rows (`has_more=false`, `next_cursor=null`)
- `limit + 1` rows (`has_more=true`, `next_cursor` set)
- consume all pages by following cursors → assert no duplicate IDs across pages
- malformed cursor → 400 `BAD_REQUEST` with code `INVALID_CURSOR`
- cursor pointing to a deleted/missing record → returns the next page (no error; the keyset just continues)

---

## 7. Realtime protocol (WebSocket)

### 7.1 Connection

`GET /v1/ws` with the same auth as REST. Uses `nhooyr.io/websocket`. JSON message envelope:

```json
{ "type": "message.new", "data": { ... } }
```

### 7.2 Server → client events

| Event | Payload |
|---|---|
| `message.new` | full Message |
| `message.edited` | Message |
| `message.deleted` | `{ message_id, conversation_id }` |
| `message.read` | `{ message_id, user_id, read_at }` |
| `conversation.created` | Conversation |
| `conversation.updated` | Conversation |
| `conversation.member_added` | `{ conversation_id, member }` |
| `conversation.member_removed` | `{ conversation_id, user_id }` |
| `presence.update` | `{ user_id, status, last_active_at }` |
| `typing.start` | `{ conversation_id, user_id }` |
| `typing.stop` | `{ conversation_id, user_id }` |
| `friend.request_received` | FriendRequest |
| `friend.request_accepted` | FriendRequest |
| `room.started` | `{ conversation_id, initiator_id, video }` — fired when first participant joins an empty room (treat as "incoming call" UX) |
| `room.participant_joined` | `{ conversation_id, user_id, video, joined_at }` |
| `room.participant_left` | `{ conversation_id, user_id }` |
| `room.video_changed` | `{ conversation_id, user_id, video }` — relayed from LiveKit track-published/unpublished |
| `room.ended` | `{ conversation_id }` — fired when last participant leaves and room empties |

### 7.3 Client → server events

| Event | Payload | Notes |
|---|---|---|
| `heartbeat` | `{}` | every 30s, foreground only |
| `typing.start` | `{ conversation_id }` | server debounces and re-broadcasts |
| `typing.stop` | `{ conversation_id }` | |
| `presence.set` | `{ status: 'online'|'sleeping' }` | manual override |

### 7.4 Hub design

- One goroutine per connection (read pump + write pump pattern).
- Per-connection bounded write channel (size 64). Slow consumers: drop oldest, force reconnect after threshold.
- `Hub` struct: `map[uuid.UUID][]*Conn` (user_id → connections), guarded by `sync.RWMutex`.
- On message creation, service publishes to Redis `conv:<id>:messages`. Each instance's hub subscribes to the channels for its connected users' active conversations.

---

## 8. Security

### 8.1 Password hashing (`internal/argon2id`)

Wrap `github.com/alexedwards/argon2id` with locked params:

```go
package argon2id

import (
    "github.com/alexedwards/argon2id"
)

var Params = &argon2id.Params{
    Memory:      64 * 1024,  // 64 MiB
    Iterations:  3,
    Parallelism: 2,
    SaltLength:  16,
    KeyLength:   32,
}

func Hash(password string) (string, error)         { return argon2id.CreateHash(password, Params) }
func Verify(password, hash string) (bool, error)   { return argon2id.ComparePasswordAndHash(password, hash) }
```

Test cases: valid hash, invalid password, malformed hash, empty password (reject).

### 8.2 Sessions (`internal/session`)

Wrap `github.com/alexedwards/scs/v2` + `github.com/alexedwards/scs/pgxstore`:

```go
package session

import (
    "time"
    "github.com/alexedwards/scs/v2"
    "github.com/alexedwards/scs/pgxstore"
    "github.com/jackc/pgx/v5/pgxpool"
)

func New(pool *pgxpool.Pool) *scs.SessionManager {
    m := scs.New()
    m.Store = pgxstore.New(pool)
    m.Lifetime = 30 * 24 * time.Hour      // 30 days
    m.IdleTimeout = 7 * 24 * time.Hour    // 7-day idle expiry
    m.Cookie.Name = "wakeup_session"
    m.Cookie.HttpOnly = true
    m.Cookie.Secure = true
    m.Cookie.SameSite = http.SameSiteLaxMode
    m.Cookie.Persist = true
    return m
}
```

The `scs` `LoadAndSave` middleware is wired at the router root. **Cookies only — no Bearer-token adapter, no JWTs anywhere.** Mobile (Expo) handles cookies via Expo's session package; both web and mobile send the same `wakeup_session` cookie, the backend doesn't distinguish.

WebSocket auth: same cookie. The `/v1/ws` upgrade is just an HTTP request — the client sends the session cookie, the upgrade handler reads it through the same `scs` middleware as REST.

**CSRF stance (locked for v1):** the cookie is `SameSite=Lax`, which the browser will not send on cross-origin POST/PUT/PATCH/DELETE requests. That is the only CSRF protection v1 implements — **no CSRF token middleware**. Native mobile clients (Expo) make explicit fetch calls and aren't subject to CSRF in the browser sense. If we ever add a browser-rendered web app that needs to make state-changing top-level navigation requests (rare), revisit. Until then, do not let CodeRabbit talk you into adding double-submit-cookie or synchronizer-token middleware — it's noise for our threat model.

### 8.3 Rate limiting (`internal/ratelimit`)

Redis token bucket (sliding window). Key: `rl:<scope>:<identifier>`. Identifier is `user_id` if authed, else IP.

Defaults applied via middleware:
- `/v1/auth/*`: 10/min per IP
- writes (POST/PATCH/PUT/DELETE): 60/min per user
- reads: 300/min per user
- WS message send: 20/sec burst, 200/min sustained

429 response uses `apierror.CodeRateLimited` and includes `Retry-After`.

### 8.4 CORS

Allowed origin: `https://wakeup.app` + Expo dev origin. `Access-Control-Allow-Credentials: true` (sessions need it).

### 8.5 Headers

`chi/middleware` + custom: `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Strict-Transport-Security` (in prod). No `X-Frame-Options` needed for an API.

### 8.6 Input validation

Every handler validates input via `validator/v10`. Validation failures → `apierror.CodeValidation` with field-level details.

### 8.7 Admin impersonation

Admins can act as another user temporarily for support and debugging. Implementation: a single field stored in the admin's existing `scs` session. **No second login flow, no second cookie, no token swap.**

**Mechanism:**

- `POST /v1/admin/users/{id}/impersonate` — admin (role=admin) puts `impersonating_user_id=<target>` into their own session via `scs.Manager.Put`. Returns the target user's `MeResponse` with `impersonated_by` populated.
- `POST /v1/admin/impersonate/end` — clears the field via `scs.Manager.Remove`. Returns the admin's own `MeResponse`.
- Auth middleware (`§4.7` step 8) reads `impersonating_user_id` after loading the session:
  - If set: `ctx.User = <impersonated user>` (the effective identity for permissions, data scoping, message attribution). `ctx.RealUser = <session owner / admin>`.
  - If not set: `ctx.User == ctx.RealUser` = the session owner.
- Handlers and services always read `ctx.User` for "who is acting." Audit logging (and only audit logging) reads `ctx.RealUser` to record the true actor.

**Restrictions (enforced at the impersonate endpoint):**

| Attempt | Response |
|---|---|
| Non-admin tries to impersonate | 403 `FORBIDDEN` |
| Admin impersonates self | 422 `VALIDATION_FAILED` |
| Admin impersonates another admin | 403 `FORBIDDEN` (no privilege escalation paths) |
| Admin impersonates soft-deleted user | 404 `RESOURCE_NOT_FOUND` |
| Admin impersonates non-existent user | 404 |
| End impersonation when not impersonating | 204 (idempotent no-op) |

**Actions blocked while impersonating** (return 403 `FORBIDDEN` with code `BLOCKED_DURING_IMPERSONATION`):

- Password change (`POST /v1/auth/password-reset/*`) — could lock the user out
- Account deletion (`DELETE /v1/users/me`) — destructive
- `POST /v1/auth/logout-all` — would orphan the admin's other sessions
- Modifying notification preferences (`PATCH /v1/users/me/notifications`) — surprising side effect for the user

Other actions (sending messages, joining rooms, updating display_name/avatar) are allowed but always logged.

**Audit logging — every action during impersonation writes to `audit_log` with:**

- `actor_id` = real admin's user ID (NEVER the impersonated user's)
- `metadata.impersonating_user_id` = the target user's ID
- `action` = the underlying action (e.g. `message.sent`) OR one of the bookend actions: `impersonate.started`, `impersonate.ended`

The `impersonate.started` and `impersonate.ended` entries are mandatory bookends for every session — even read-only impersonation produces them.

**`GET /v1/auth/me` semantics:**

| State | Returns |
|---|---|
| Not impersonating | Admin's own `MeResponse`, `impersonated_by: null` |
| Impersonating | Target user's `MeResponse`, `impersonated_by: { id, username, display_name }` of the admin |

Frontend uses `impersonated_by != null` to render a persistent banner like `You are impersonating @caden as Caden Lund — [End impersonation]`.

**Test matrix (folded into §12.5 for the impersonate handler + the auth middleware):**

- Non-admin attempts impersonate → 403
- Admin impersonates self → 422
- Admin impersonates another admin → 403
- Admin impersonates non-existent / soft-deleted user → 404
- Admin impersonates valid user → 200; subsequent `/v1/auth/me` returns target with `impersonated_by` populated
- During impersonation: send a message → succeeds; audit row has `actor_id = admin`, metadata has `impersonating_user_id = target`
- During impersonation: password change → 403 `BLOCKED_DURING_IMPERSONATION`
- During impersonation: account delete → 403 `BLOCKED_DURING_IMPERSONATION`
- During impersonation: logout-all → 403 `BLOCKED_DURING_IMPERSONATION`
- End impersonation → 200; subsequent `/v1/auth/me` returns admin's own response, no `impersonated_by`
- End impersonation when not impersonating → 204
- Audit entries for `impersonate.started` and `impersonate.ended` exist in correct order

**New apierror code:**

```go
CodeBlockedDuringImpersonation Code = "BLOCKED_DURING_IMPERSONATION"
// Maps to HTTP 403.
```

---

## 9. Storage & media (`internal/objectstore`)

```go
type ObjectStore interface {
    // Put writes body to the bucket at key. The contentType passed here MUST be the
    // server-detected MIME (see §9.2), not the client-claimed one. Implementations
    // set it as the object's Content-Type header for use by the presigner.
    Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error

    // PresignGet returns a short-lived URL that GETs the object. The optional
    // contentDisposition (e.g. `attachment; filename="report.pdf"`) is bound into
    // the signed URL via S3's `response-content-disposition` query param so the
    // browser sees the original user-supplied filename and never tries to render
    // an attachment inline. Pass an empty string for avatars (default to inline).
    PresignGet(ctx context.Context, key string, ttl time.Duration, contentDisposition string) (string, error)

    Delete(ctx context.Context, key string) error
}
```

One implementation: `s3Store` using `aws-sdk-go-v2`. Locally points at MinIO (`http://localhost:9000`). In prod points at AWS S3 / Tigris / R2. Same code path.

### 9.1 Storage key conventions (UUID-only — never user-supplied)

- **Avatars:** `avatars/{user_id}/{uuid}.{ext}` — max 5 MB, `image/*` only. `{ext}` is derived from the **server-detected** MIME, never from the client-uploaded filename. See §9.5 for the public-vs-presigned decision (open).
- **Attachments:** `attachments/{conversation_id}/{message_id}/{attachment_id}` — max 50 MB. **No filename in the key.** The original filename is stored in `attachments.filename` and surfaced to the client only via the DTO + the signed URL's `response-content-disposition`.

Why UUID-only keys: user-supplied filenames carry a path-traversal / injection / unicode-mischief surface that we don't need to take on. The DB row holds the human-readable filename and content type; the S3 key is opaque.

### 9.2 Upload model (locked: server-proxied)

V1 uses **server-proxied uploads.** Client posts `multipart/form-data` to `POST /v1/users/me/avatar` or `POST /v1/attachments`. Backend reads the body, validates, writes to S3, and returns the DTO. We do NOT issue presigned PUTs in v1 (deferred — see "Open decisions" §9.5).

The handler MUST:

1. Wrap the request body in `http.MaxBytesReader(w, r.Body, max+1<<10)` *before* calling `r.ParseMultipartForm`. This caps the bytes the server will buffer so an attacker cannot stream 50 GB and trash the host. `max` is 5 MiB for avatars, 50 MiB for attachments. The `+1KB` slack lets multipart framing fit.
2. After `ParseMultipartForm`, peek at the file: read the first 512 bytes via `io.ReadAll(io.LimitReader(file, 512))`, hand to `http.DetectContentType` (or the `mimetype` library), and reject with `422 VALIDATION_FAILED { content_type: ... }` if the detected MIME isn't on the route's allowlist. `Seek(0, io.SeekStart)` the file before passing to `objectstore.Put`.
3. Pass the **detected** MIME (not the multipart-supplied one) to `objectstore.Put` and to `attachments.content_type` in the DB.
4. Sanitize `filename` for storage in DB: strip path separators (`/`, `\`), control chars, NUL bytes; truncate to 255 chars; reject empty after sanitization.

**Backend proxies upload bytes; backend never proxies download bytes.** Downloads are always via short-lived presigned GETs.

### 9.3 Download authorization (mandatory)

`GET /v1/attachments/{id}` is auth-gated *and* permission-gated. Before issuing a presigned URL the service MUST verify the caller is a member of at least one conversation that contains a message linked to this `attachment_id` via `message_attachments`. If no such link exists (orphaned upload) the uploader is the only one allowed to GET. On any failure return `404 RESOURCE_NOT_FOUND` (do not leak existence) — never `403`.

Avatars are addressed separately (§9.5) — for v1 they are public profile images, so `UserResponse.avatar_url` is a stable URL not gated by per-request authz.

Presigned URL TTL: **5 minutes**. Issue the URL with the original filename baked into `response-content-disposition` (see §9.0 interface) so the browser sees the human-readable name on download. Never embed the original filename in the *key* itself (see §9.1).

### 9.4 Bucket configuration baseline (prod)

The backend writes to a single bucket (e.g. `wakeup-prod-media`) with:

- **Block all public ACLs** at the bucket level (`PublicAccessBlockConfiguration` all-true).
- Default object ACL: **private**. Public access is opt-in per-prefix via bucket policy (only `avatars/*` if §9.5 picks the public-CDN path).
- **Server-side encryption (SSE-S3)** enabled by default at the bucket level.
- **TLS-only** bucket policy: deny `s3:*` when `aws:SecureTransport: false`.
- **CORS:** allow `GET` only, from the production origins; do not allow `PUT`/`POST` directly (we don't presign uploads in v1).
- IAM role for the backend grants only `s3:PutObject`, `s3:GetObject`, `s3:DeleteObject` on `arn:aws:s3:::wakeup-prod-media/*` — no bucket-level `s3:*`, no listing.

For local dev (MinIO) these are not enforced; the same code path works against MinIO with admin creds.

### 9.5 Open decisions (need operator input before Phase 7)

| # | Decision | Default if you say nothing |
|---|---|---|
| 9.5.a | **Avatar URL strategy.** Stable public URL via CDN/S3-public-prefix vs short-lived presigned GET refreshed by the client. Public is faster + cacheable, but every avatar URL is then permanently fetchable by anyone. Presigned needs frontend to refresh on URL expiry. | Stable public URL on `avatars/*` prefix, behind the CDN in prod. Avatars are public profile images by design (visible to non-friends in search results). |
| 9.5.b | **Avatar re-encoding.** Decode → resize to max 512×512 → re-encode (PNG/WebP) on upload. Strips EXIF (geo location, etc.), prevents polyglot files (PNG that's also valid JS), kills decompression bombs. Adds a Go image dep. | Yes — re-encode. The privacy and XSS-prevention wins outweigh the dep. |
| 9.5.c | **Attachment scanning.** No virus / malware scan in v1 (consistent with "out of scope" ethos). Document explicitly so it's a known posture. | Document v1 does not scan; do not promise it as a corporate file-transfer tool. |

### 9.6 Orphan cleanup

`internal/job/` adds an `attachment-orphan-sweeper` running every 1 hour: deletes `attachments` rows + the corresponding S3 objects where `created_at < now() - 24h` AND no `message_attachments` row references the attachment. Prevents leak from clients that upload-then-abandon.

### 9.7 Response shape for `GET /v1/attachments/{id}`

Returns a structured DTO, not just a URL:

```json
{
  "id": "0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c",
  "url": "https://wakeup-prod-media.s3.us-east-1.amazonaws.com/attachments/...?X-Amz-Signature=...",
  "expires_at": "2026-05-02T09:36:21.810Z",
  "filename": "Q1-report.pdf",
  "content_type": "application/pdf",
  "size_bytes": 1048576
}
```

This lets the frontend show a download UI before fetching, and re-request a fresh URL when the previous one expires (compare `expires_at` to current time).

---

## 10. External services (email, push, voice/video)

### 10.1 Email (`internal/mailer`)

Wraps Resend. Single use case: password reset.

```go
type Mailer interface {
    SendPasswordReset(ctx context.Context, to, token string) error
}
```

Templates inline in Go (no template files for v1). Use Resend's React Email or just HTML strings — keep it simple.

### 10.2 Push notifications (`internal/pushnotif`)

Wraps the Expo Push HTTP API (no SDK needed, just a JSON POST):

```go
type Pusher interface {
    Send(ctx context.Context, tokens []string, n Notification) error
}

type Notification struct {
    Title string
    Body  string
    Data  map[string]any
}
```

Fired by `MessageService.Send` when the recipient has no active WS connection. Look up active connections via Redis (Hub publishes `presence:online:<user_id>` keys with TTL on heartbeat).

### 10.3 Voice & video (`internal/room`) — self-hosted LiveKit

**Model: every conversation has a persistent voice/video room. `room_id == conversation_id`.** No separate room entity. The room exists conceptually as long as the conversation exists; physically it spins up in LiveKit on first join and tears down when empty.

- **Default audio-only on join.** The client publishes microphone, does NOT publish camera. Any participant can enable their camera mid-call by publishing the camera track — no server round-trip needed; LiveKit handles the renegotiation.
- **"Calling" UX = first person joins** → other conversation members get a `room.started` WebSocket event (also pushed via Expo if offline). Treat as the "incoming call" notification.
- **"In a call" indicator** = anyone listed in the current participants set is visible to other conversation members.
- **No "end call" concept** for the room itself. Individual users leave; when last user leaves, the room is empty. LiveKit emits `room_finished`; we broadcast `room.ended`.

**Backend responsibilities:**

1. **Issue LiveKit JWT room tokens** (`internal/service/room/Join`). Token grants:
   - `room`: `conv:<conversation_id>`
   - `roomJoin: true`
   - `canPublish: true`, `canPublishSources: ["microphone", "camera"]`
   - `canSubscribe: true`
   - `canPublishData: true` (for in-call data messages, e.g. emoji reactions)
   - TTL: 10 minutes (LiveKit auto-refreshes during the connection)
   - Identity: stable per user (`user:<user_id>`) so `participant_joined` events can be mapped back
   - Metadata: `{"display_name": "...", "avatar_url": "...", "video": false}`
2. **Authorize the join** — only conversation members can request a token. Use the conversation member check from `conversation.Service`.
3. **Receive LiveKit webhooks** at `POST /webhooks/livekit`:
   - Verify the signature using LiveKit's `webhook.NewReceiver(apiKey, apiSecret).Receive(req)`.
   - Handle: `room_started`, `participant_joined`, `participant_left`, `track_published` (camera only), `track_unpublished` (camera only), `room_finished`.
4. **Mirror room state in Redis** (`packages/pubsub` or directly via redis client):
   - `room:<conv_id>:participants` SET of `user_id`
   - `room:<conv_id>:started_at` ISO timestamp (set on first join)
   - `room:<conv_id>:participant:<user_id>:video` `"true" | "false"`
   - All keys have a 24-hour TTL as a safety net for stuck state.
5. **Broadcast `room.*` events** (§7.2) to conversation members via the existing pubsub broker.
6. **Trigger push notifications** on `room.started` for every conversation member who is offline AND has the relevant notification preference enabled (`calls` category).

**Webhook endpoint signature verification — example:**

```go
// internal/handler/http/livekit_webhook_handler.go
//
// /webhooks/livekit handler — UNAUTHENTICATED, verified by LiveKit signature.
// Add /webhooks/livekit to the no-auth allowlist in §4.7.

import "github.com/livekit/protocol/auth"
import "github.com/livekit/protocol/webhook"

type LiveKitWebhookHandler struct {
    svc        *room.Service
    lkReceiver *webhook.Receiver  // verifies LiveKit's HMAC-signed Authorization header
}

func NewLiveKitWebhookHandler(svc *room.Service, apiKey, apiSecret string) *LiveKitWebhookHandler {
    // The receiver wraps an auth.SimpleKeyProvider seeded with the same
    // (apiKey, apiSecret) pair LiveKit was started with. It validates every
    // inbound webhook's Authorization header against that key.
    keyProvider := auth.NewSimpleKeyProvider(apiKey, apiSecret)
    return &LiveKitWebhookHandler{
        svc:        svc,
        lkReceiver: webhook.NewReceiver(keyProvider),
    }
}

func (h *LiveKitWebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
    event, err := h.lkReceiver.Receive(r)
    if err != nil {
        WriteError(w, r, apierror.Unauthorized("invalid livekit webhook signature"))
        return
    }
    if err := h.svc.HandleWebhookEvent(r.Context(), event); err != nil {
        WriteError(w, r, err)
        return
    }
    w.WriteHeader(http.StatusOK)
}
```

In `cmd/server/main.go`, construct it once with the env-loaded `LIVEKIT_API_KEY` and `LIVEKIT_API_SECRET`, then mount: `router.Post("/webhooks/livekit", lkWebhookHandler.Handle)`.

**Self-hosted LiveKit — runs in our `docker-compose.yml`:**

```yaml
livekit:
  image: livekit/livekit-server:latest
  ports:
    - "7880:7880"           # signaling (WebSocket)
    - "7881:7881/tcp"       # TCP fallback for media
    - "7882:7882/udp"       # embedded TURN
    - "50000-50100:50000-50100/udp"  # media (RTP)
  command: ["--dev", "--bind=0.0.0.0"]
  environment:
    LIVEKIT_KEYS: "devkey: devsecretdevsecretdevsecret"   # API key: API secret (≥32 chars)
  # NOTE: --dev mode logs all events and uses sane defaults.
  # For production deploy (out of v1 scope), mount a real livekit.yaml
  # with proper TURN config, webhook URL, and rotated keys.
```

`LIVEKIT_URL=ws://localhost:7880`, `LIVEKIT_API_KEY=devkey`, `LIVEKIT_API_SECRET=devsecretdevsecretdevsecret` for local dev. Production deployment lives outside the v1 build (the spec stops at "ready for mobile"); when it happens, the same docker-compose pattern works on a Hetzner VPS with a real config file mounted.

**LiveKit webhook URL configuration** — for local dev where Docker compose can reach the backend directly, add to LiveKit config (or via env):
```yaml
webhook:
  api_key: devkey
  urls:
    - http://host.docker.internal:8080/webhooks/livekit  # Docker on Mac/Win
    # or http://172.17.0.1:8080/webhooks/livekit  on Linux
```

In tests, use `livekit/server-sdk-go`'s testing helpers — fire a fake webhook event into the handler with a valid signature and assert the resulting Redis state + WS broadcast.

**Permissions checklist (test exhaustively per §12.5):**
- Non-member tries to join → 403
- Member joins direct conversation → 200, token issued
- Member joins group → 200, token issued
- Webhook with bad signature → 401
- Webhook with valid signature for unknown room → 200, no-op (don't leak)

---

## 11. Monitoring

- **Sentry** for errors. `sentry-go` middleware on the chi router. Init in `main.go` from `SENTRY_DSN` env var.
- **Fly built-in metrics** for request rate, RAM, CPU, restarts. Free, automatic. Linked to a Grafana Cloud workspace.
- **`slog` JSON output** to stdout. Fly aggregates logs.
- **No Prometheus, no OpenTelemetry tracing** in v1. Add later if scale demands.
- **Health endpoints:**
  - `/v1/healthz` — liveness, no deps
  - `/v1/readyz` — pings db + redis, returns 503 if either down

---

## 12. Testing strategy

### 12.1 Hard rules

- Every package has `*_test.go` covering happy path + every error path.
- Every test uses `t.Parallel()`.
- `go test -race -count=10 ./...` must pass cleanly.
- Repository tests use real Postgres via `testcontainers-go`, isolated per test via `pgtestdb`.
- Service tests compose real repos against a test DB.
- Handler tests use `httptest.NewServer` with the real router + real services + real DB.
- WebSocket tests dial a real upgraded connection and assert envelope round-trips.
- Coverage gate: `go test -cover` must report ≥80% per package. CI fails below.

### 12.2 testutil package

```go
// internal/testutil/db.go
package testutil

func NewTestDB(t *testing.T) *pgxpool.Pool {
    t.Helper()
    return pgtestdb.New(t, pgtestdb.Config{
        DriverName: "pgx",
        ...
    }, pgtestdb.NoopMigrator{}) // migrations applied to template
}
```

Template DB is created once per test binary, migrations run once, every test gets a clone in <50ms.

### 12.3 testcontainers setup

```go
// internal/testutil/containers.go
package testutil

func StartPostgres(t *testing.T) string                       { ... }   // returns DSN
func StartRedis(t *testing.T) string                          { ... }   // real redis (NOT miniredis) — needed for cross-process pubsub in §12.7 multi-instance tests
func StartMinIO(t *testing.T) string                          { ... }
func StartLiveKit(t *testing.T, webhookURL string) LiveKitTestEnv { ... }   // see §12.8
```

Containers are reused across tests in the same package via `sync.Once` per package. LiveKit boot is the slowest (~2s) — pay the cost once per test binary.

### 12.4 Edge cases to test exhaustively

For every domain, write tests for:

- **Pagination:** empty page, exactly-`limit` page, malformed cursor, cursor pointing to deleted record.
- **Auth:** missing token, expired session, revoked session, wrong role.
- **Concurrency:** simultaneous friend requests, double-accept, sending to deleted conversation.
- **Soft delete:** deleted user invisible to lists, still accessible by ID for message history attribution.
- **Validation:** every required field missing, every length boundary, every enum invalid value.
- **Permissions:** can't read another user's DM, can't promote yourself to group admin, can't message a blocked user.
- **Idempotency:** duplicate friend request, duplicate read receipt at the domain level (separate from the middleware test in §12.5).

### 12.5 Handler test discipline (exhaustive — every status code, every failure path)

**This is non-negotiable.** For every HTTP handler, the corresponding `_test.go` is a single table-driven test that proves every documented status code is reachable AND every error response is shaped correctly. The matrix below is the *minimum* set of subtests per handler — add more if the endpoint has unique semantics.

**Per handler, write subtests for every applicable row:**

| Category | Subtest | Asserts |
|---|---|---|
| **Happy path** | success | response status matches swagger; full body shape; every field type; required headers (`X-Request-ID` echoed) |
| **Auth** | no auth header / no cookie | 401 `UNAUTHORIZED` |
|  | expired session | 401 |
|  | revoked session (logged out) | 401 |
|  | tampered/garbage cookie value | 401 |
| **Authorization** | non-admin hits admin route | 403 `FORBIDDEN` |
|  | user accesses another user's resource | 403 or 404 (per documented behavior — pick one and stick with it) |
| **Validation** (body endpoints) | each required field missing individually | 422 `VALIDATION_FAILED` with `fields[].field` correct |
|  | each enum field with invalid value | 422, field-level code |
|  | each length-constrained field at min−1 and max+1 | 422 |
|  | each format-constrained field (email, uuid, url) with invalid format | 422 |
|  | empty body where body required | 400 `BAD_REQUEST` |
|  | malformed JSON | 400 |
|  | wrong content-type | 415 (or 400 with documented code) |
| **Not found** | resource ID nonexistent | 404 `RESOURCE_NOT_FOUND` |
|  | resource ID exists but soft-deleted | 404 (soft-deleted == hidden) |
| **Conflict** | duplicate (e.g. username on register, friend request, group member) | 409 `CONFLICT` |
| **Rate limit** | burst over the route's limit | 429 `RATE_LIMITED` with `Retry-After` header AND `retry_after_seconds` in body |
| **Payload** | upload >max bytes | 413 `PAYLOAD_TOO_LARGE` |
|  | disallowed content-type on upload | 422 |
| **Idempotency** (POST/PATCH/PUT) | same key + same body → cached replay | 200 (or original status) with `Idempotent-Replay: true` header; handler invoked exactly once across the two requests |
|  | same key + different body | 422 `IDEMPOTENCY_KEY_REUSED` |
|  | no key | normal flow, no caching |
|  | body >256 KB with key | `Idempotent-Replay: skipped` header, handler runs |
| **Internal failure** | inject service error (use a fake repo returning `errors.New("boom")`) | 500 `INTERNAL`; response body has GENERIC message (no leaked `boom`); slog line written; Sentry transport captured the event |

**Additional discipline rules:**

- Every subtest is named with the pattern `Test<Handler>/<Category>_<scenario>` (e.g. `TestCreateMessage/validation_body_missing`). Subtest names appear in CI logs — make them grep-able.
- Every error subtest asserts the **full response envelope** (`error.code`, `error.message`, `error.fields` where applicable) — not just the status code.
- Every subtest asserts the `X-Request-ID` response header is non-empty.
- Use a `harness` helper in `internal/testutil/` that spins up the full router + real services + pgtestdb + miniredis (or testcontainers redis) + fake mailer/pusher/objectstore. One harness, reused across every handler test.
- **CI gate:** a small shell check (`scripts/ci/check-handler-tests.sh`) parses each handler's swaggo `@Failure` annotations and grep-counts matching subtests in the `_test.go`. CI fails if subtest count < documented status count. Implement this in Phase 13.

### 12.6 Test harness + fixtures (`internal/testutil/`)

Without these, every handler test reinvents 30 lines of setup. With them, every test is 5–10 lines.

**Harness — `internal/testutil/harness.go`:**

```go
package testutil

type Harness struct {
    Server  *httptest.Server
    DB      *pgxpool.Pool
    Redis   redis.UniversalClient
    Mailer  *FakeMailer       // captures sent emails
    Pusher  *FakePusher       // captures sent push notifications
    Storage *FakeObjectStore  // in-memory bucket
    Hub     *ws.Hub
    Sentry  *SentryRecorder   // captures events
    Cfg     config.Config
}

// New starts a fully-wired server backed by per-test pgtestdb + miniredis +
// fakes for external services. t.Cleanup tears down. Safe to call from
// parallel tests — each call gets an isolated database and Redis namespace.
func New(t *testing.T) *Harness {
    t.Helper()
    ...
}

// HTTPClient returns a client cookie-jared to the test server. Anonymous.
func (h *Harness) HTTPClient(t *testing.T) *http.Client { ... }

// AuthClient registers + logs in as a fixture user, returns an authenticated client.
// Returns the user too so the test can reference IDs.
func (h *Harness) AuthClient(t *testing.T, opts ...fixtures.UserOpt) (*http.Client, user.User) { ... }

// AdminClient is AuthClient with role=admin pre-set.
func (h *Harness) AdminClient(t *testing.T) (*http.Client, user.User) { ... }

// WSDial dials /v1/ws authenticated as the given user.
// Returns a connection plus a helper Receive(ctx) that decodes one envelope.
func (h *Harness) WSDial(t *testing.T, c *http.Client) *ws.TestConn { ... }
```

**Fixtures — `internal/testutil/fixtures/`:**

```go
package fixtures

// UserOpt configures MakeUser. Functional options.
type UserOpt func(*userBuilder)

func WithUsername(s string) UserOpt        { ... }
func WithEmail(s string) UserOpt           { ... }
func WithDisplayName(s string) UserOpt     { ... }
func WithPassword(s string) UserOpt        { ... }
func WithRole(role string) UserOpt         { ... }
func WithColorScheme(s string) UserOpt     { ... }
func WithSoftDeleted() UserOpt             { ... }

// MakeUser inserts a user with sensible defaults (random username, "Password123!" hash, etc.).
// Returns the persisted user.User. t.Cleanup is NOT used — the test's pgtestdb is dropped at end of test.
func MakeUser(t *testing.T, db *pgxpool.Pool, opts ...UserOpt) user.User { ... }

// MakeFriendship creates a friendship in the given status. Default 'accepted'.
func MakeFriendship(t *testing.T, db *pgxpool.Pool, a, b user.User, opts ...FriendOpt) friendship.Friendship { ... }

// MakeConversation creates a direct or group conversation with the given members.
// Type is inferred from len(members): 2 → direct, 3+ → group (name required via WithName).
func MakeConversation(t *testing.T, db *pgxpool.Pool, members []user.User, opts ...ConvOpt) conversation.Conversation { ... }

// MakeMessage inserts a message in the given conversation from the given sender.
// Default body "test message" — override via WithBody.
func MakeMessage(t *testing.T, db *pgxpool.Pool, conv conversation.Conversation, sender user.User, opts ...MsgOpt) message.Message { ... }

// MakeAttachment uploads an in-memory file to the FakeObjectStore and creates the row.
func MakeAttachment(t *testing.T, h *Harness, uploader user.User, opts ...AttOpt) attachment.Attachment { ... }
```

**Discipline:**
- Every handler test starts with `h := testutil.New(t)`.
- Every domain test that needs entities calls into `fixtures.MakeX(...)`.
- No test creates entities by hand-rolled SQL or by hand-built service calls.
- Fixture defaults are deterministic-randomized: usernames are `fixturer-<short-uuid>`, display names `Test User <short-uuid>`. This avoids collisions while keeping tests reproducible enough to debug.

**Example handler test using the harness + fixtures + matrix from §12.5:**

```go
func TestUpdateMe(t *testing.T) {
    t.Parallel()
    h := testutil.New(t)

    cases := []struct {
        name    string
        client  func(t *testing.T) *http.Client
        body    any
        status  int
        errCode string
    }{
        {
            name:   "success_color_scheme",
            client: func(t *testing.T) *http.Client { c, _ := h.AuthClient(t); return c },
            body:   map[string]any{"color_scheme": "dark"},
            status: 200,
        },
        {
            name:    "validation_invalid_color_scheme",
            client:  func(t *testing.T) *http.Client { c, _ := h.AuthClient(t); return c },
            body:    map[string]any{"color_scheme": "fuschia"},
            status:  422,
            errCode: "VALIDATION_FAILED",
        },
        {
            name:    "auth_no_token",
            client:  func(t *testing.T) *http.Client { return h.HTTPClient(t) },
            body:    map[string]any{"display_name": "x"},
            status:  401,
            errCode: "UNAUTHORIZED",
        },
        // ... rest of the matrix per §12.5
    }

    for _, tc := range cases {
        tc := tc
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()
            // ... do the request, assert status + envelope shape + X-Request-ID header
        })
    }
}
```

### 12.7 WebSocket test discipline (exhaustive — every event, every permission boundary)

The realtime layer is harder to test than HTTP and easier to break in production. This matrix exists so it doesn't.

**Required harness primitives (extend `internal/testutil/harness.go` per §12.6):**

```go
// NewMultiInstance returns N harnesses sharing one Postgres + one Redis (via
// real testcontainers, NOT miniredis — miniredis pubsub doesn't fan across
// processes). Each harness runs its own httptest.Server. Used to prove the
// stateless-API + Redis-pubsub fan-out story works end-to-end.
func NewMultiInstance(t *testing.T, n int) []*Harness

// TestConn wraps the websocket connection with helpers for assertions.
type TestConn struct{ /* ... */ }

// Receive reads one envelope from the connection with the given timeout.
// Returns the typed payload + the raw message for shape assertions.
func (c *TestConn) Receive(ctx context.Context, expectedType string) (data []byte, raw wsproto.Envelope, err error)

// MustNotReceive asserts that NO message of the given type arrives within d.
// This is the "does NOT fire" primitive — critical for permission tests.
func (c *TestConn) MustNotReceive(t *testing.T, eventType string, d time.Duration)

// Send writes a client→server envelope.
func (c *TestConn) Send(t *testing.T, eventType string, data any)
```

**Per-event matrix — every event in §7.2 gets all five rows:**

| Category | Subtest |
|---|---|
| **fires_for_recipients** | The originating REST/service call delivers the event to every entitled recipient (member, friend, etc.). Assert via `Receive` on each recipient's TestConn. |
| **does_not_fire_for_outsiders** | Spawn an unrelated user; assert `MustNotReceive` for the event type within 500ms. **This is the critical permission test.** |
| **payload_shape** | Decode the raw envelope; assert every field in §7.2 is present with the correct type and no extra fields leak. Use a `requiredFields` table per event. |
| **multi_instance_fanout** | Use `NewMultiInstance(t, 2)`. User A connects to harness[0], User B to harness[1]. Action on A → B receives the event via Redis pubsub. **Without this test, the stateless prod story is theoretical.** |
| **idempotent_under_repeat** | Same action twice (e.g., two duplicate friend requests) → recipient gets the event exactly once OR an explicit dedup signal. Per-event policy (define in handler tests). |

**Per-event coverage list — every row in §7.2 needs the five subtests above plus event-specific cases:**

- `message.new` — also: deleted-conversation member doesn't receive; sender DOES receive (echo-back for multi-device).
- `message.edited`, `message.deleted` — also: only members of the conversation receive.
- `message.read` — fires for the message *sender only* (so they see read state). Other recipients don't get it.
- `conversation.created` — fires for every initial member.
- `conversation.member_added` — fires for the existing members AND the newly added one.
- `conversation.member_removed` — fires for the remaining members AND the one being removed (so their UI dismisses).
- `presence.update` — fires for **friends only**; never for non-friends; never for blocked users; not echoed back to the source user.
- `typing.start` / `typing.stop` — fires for conversation members; **server debounces** — multiple `start` events from same user within 3s = single broadcast (assert with timing).
- `friend.request_received` — fires for the addressee only.
- `friend.request_accepted` — fires for the requester only.
- `room.started` — fires for every conversation member EXCEPT the initiator (they're already in the room).
- `room.participant_joined` / `left` — fires for every other participant currently in the room.
- `room.video_changed` — fires for every other participant in the room.
- `room.ended` — fires for every conversation member when last participant leaves.

**Connection lifecycle subtests (one block per concern, all under `TestWebSocketLifecycle`):**

- `upgrade_no_cookie` — GET `/v1/ws` with no cookie → 401, no upgrade.
- `upgrade_expired_cookie` — cookie value past expiry → 401.
- `upgrade_tampered_cookie` — cookie with garbage value → 401.
- `upgrade_valid_cookie` — 101 Switching Protocols.
- `heartbeat_updates_db` — client sends `heartbeat`; assert `presence_states.last_heartbeat_at` updated within 100ms.
- `stale_presence_decays` — fast-forward time (use a clock injected into presence service); assert online → away after 5min, away → offline after 1h. Verify `presence.update` events fire on both transitions.
- `typing_debounce` — send 5× `typing.start` from same user within 3s; assert other members receive exactly 1 broadcast.
- `disconnect_publishes_presence_change` — close connection cleanly; assert `presence.update` (online → away if no other connections) is broadcast to friends within 1s.
- `slow_consumer_kicked` — wedge a connection's read pump (use a custom slow `TestConn`); fire 100 events; assert connection is closed after the 64-deep write buffer overflows.
- `simultaneous_connections_same_user` — open 2 WS connections as same user; both receive every event for that user. Closing one does NOT flip presence (other still active).
- `reconnect_no_replay` — connect, disconnect, send 3 messages while disconnected, reconnect; assert NO replayed messages arrive (per §6 conventions: client re-fetches via REST).

**CI gate:** add to `scripts/ci/check-handler-tests.sh` a sibling `scripts/ci/check-ws-tests.sh` that grep-counts subtests for each event in §7.2 and fails if any row in the matrix is missing.

### 12.8 Voice & video test discipline (LiveKit, end-to-end)

Voice/video is the part of the backend the frontend can't easily smoke-test in a "Try it out" Swagger session. So we have to prove it works at the backend layer with **real LiveKit**, not mocks. The whole point of testing this exhaustively is so when your friend goes to build the Expo app, they can trust the backend is correct.

**Required harness primitive (extend testcontainers in §12.3):**

```go
// internal/testutil/containers.go
//
// StartLiveKit boots a real livekit/livekit-server container (--dev mode)
// configured with webhook URL pointing at the test harness's HTTP server.
// Returns connection details + a *webhook.Receiver pre-configured with the
// matching keys (so tests can also synthesize valid webhook events).
func StartLiveKit(t *testing.T, webhookURL string) LiveKitTestEnv

type LiveKitTestEnv struct {
    URL        string                  // ws://host:port
    APIKey     string
    APISecret  string
    Receiver   *webhook.Receiver       // for synthesizing test webhook calls
}

// Reused via sync.Once per test package — LiveKit boot is ~2s.
```

**Harness extension:**

```go
// LiveKitClient connects to the test LiveKit container as a participant.
// Used by integration tests to verify the full backend → LiveKit → webhook → state loop.
func (h *Harness) LiveKitClient(t *testing.T, identity, token string) *lksdk.Room
```

**Test layers (each layer testable in isolation, AND end-to-end):**

#### 12.8.1 Token issuance (`internal/service/room/` unit tests)

Pure unit tests. No LiveKit server needed.

| Subtest | Asserts |
|---|---|
| `token_structure` | Decoded JWT has correct claims: `room=conv:<id>`, `roomJoin=true`, `canPublish=true`, `canPublishSources=[microphone, camera]`, `canSubscribe=true`, `canPublishData=true` |
| `token_identity_stable` | Identity field is `user:<user_id>` (so participant_joined webhooks map back) |
| `token_metadata` | Metadata JSON contains `display_name`, `avatar_url`, `video` flag |
| `token_ttl` | `exp` claim is exactly 10 minutes from `iat` |
| `token_video_flag_propagated` | Calling `Join(..., video=true)` sets the metadata `video=true`; `false` sets it false. (Token permissions identical either way — flag is UI hint only.) |

#### 12.8.2 Authorization (`internal/service/room/` + handler tests)

| Subtest | Asserts |
|---|---|
| `non_member_join_forbidden` | User not in conversation → 403 |
| `direct_member_join_ok` | Both users in a direct conversation can join |
| `group_member_join_ok` | Any group member can join |
| `removed_member_join_forbidden` | User who was removed from the group → 403 |
| `soft_deleted_user_join_forbidden` | Soft-deleted user → 401 (their session is invalid anyway) |

#### 12.8.3 Webhook handler (`internal/handler/http/livekit_webhook_handler_test.go`)

Use the `Receiver` from `LiveKitTestEnv` to **synthesize valid webhook signatures**. No real LiveKit server needed for these — we're testing OUR handler.

| Subtest | Asserts |
|---|---|
| `signature_valid` | Synthesize a `participant_joined` event with valid HMAC; handler returns 200 |
| `signature_invalid` | Strip the Authorization header; handler returns 401 |
| `signature_tampered` | Mutate the body after signing; handler returns 401 |
| `room_started_updates_redis` | Event for room `conv:<id>` → Redis `room:<id>:started_at` set, `room:<id>:participants` includes user |
| `room_started_broadcasts_ws` | Same event → WS `room.started` broadcast to all conversation members EXCEPT the initiator |
| `room_started_triggers_push` | Conversation members who are offline AND have `notification_preferences.calls = true` get an Expo Push (verify via FakePusher in harness) |
| `participant_joined` | Redis SET adds user_id; WS `room.participant_joined` broadcasts to other participants |
| `participant_left` | Redis SET removes user_id; WS broadcasts; if SET becomes empty, `room.ended` fires |
| `track_published_camera` | WS `room.video_changed` broadcasts with `video=true`; non-camera tracks (microphone, screen) don't trigger this event |
| `track_unpublished_camera` | WS `room.video_changed` broadcasts with `video=false` |
| `unknown_room` | Webhook for a room ID that doesn't match any conversation → 200, no-op (don't leak existence) |

#### 12.8.4 End-to-end integration (`internal/testutil/integration/livekit_e2e_test.go`)

The proof-of-life test. Real LiveKit container, real backend, real client SDK. This is the test that lets you say "the backend works perfectly before we start the frontend."

```go
func TestLiveKit_EndToEnd(t *testing.T) {
    t.Parallel()
    h := testutil.New(t)            // also boots LiveKit testcontainer

    // 1. Two users, friends, in a group conversation.
    alice := fixtures.MakeUser(t, h.DB)
    bob   := fixtures.MakeUser(t, h.DB)
    conv  := fixtures.MakeConversation(t, h.DB, []domain.User{alice, bob}, fixtures.WithName("test-group"))

    // 2. Both users open WS connections.
    aliceWS := h.WSDial(t, h.AuthClient(t, alice))
    bobWS   := h.WSDial(t, h.AuthClient(t, bob))

    // 3. Alice POSTs to /v1/conversations/{id}/room/join (audio-only).
    aliceRoom := joinRoom(t, h.AuthClient(t, alice), conv.ID, false /* video */)

    // 4. Alice connects to LiveKit with the issued token.
    aliceLK := h.LiveKitClient(t, "user:"+alice.ID.String(), aliceRoom.LiveKitToken)
    require.NoError(t, aliceLK.Connect())
    require.NoError(t, aliceLK.LocalParticipant.PublishMicrophone())

    // 5. LiveKit fires webhook → backend updates Redis → broadcasts room.started to Bob.
    bobWS.Receive(t, "room.started", 2*time.Second)
    aliceWS.MustNotReceive(t, "room.started", 500*time.Millisecond) // initiator doesn't get this

    // 6. Bob also joins.
    bobRoom := joinRoom(t, h.AuthClient(t, bob), conv.ID, false)
    bobLK := h.LiveKitClient(t, "user:"+bob.ID.String(), bobRoom.LiveKitToken)
    require.NoError(t, bobLK.Connect())
    aliceWS.Receive(t, "room.participant_joined", 2*time.Second)

    // 7. Verify GET /v1/conversations/{id}/room shows both users.
    state := getRoomState(t, h.AuthClient(t, alice), conv.ID)
    assert.Len(t, state.Participants, 2)

    // 8. Alice enables her camera mid-call.
    require.NoError(t, aliceLK.LocalParticipant.PublishCamera())
    bobWS.Receive(t, "room.video_changed", 2*time.Second)

    // 9. Alice disconnects.
    aliceLK.Disconnect()
    bobWS.Receive(t, "room.participant_left", 2*time.Second)

    // 10. Bob disconnects → room is empty → room.ended fires for all conversation members.
    bobLK.Disconnect()
    bobWS.Receive(t, "room.ended", 2*time.Second)

    // 11. Redis state cleaned up.
    assert.Zero(t, h.Redis.SCard(ctx, "room:"+conv.ID.String()+":participants").Val())
}
```

This single test exercises: token issuance → LiveKit auth → webhook signature verification → Redis state mirroring → WS pubsub fan-out → push notification gating → room lifecycle from start to end.

**Run cost:** ~10 seconds per `TestLiveKit_EndToEnd` invocation (LiveKit container boot is the long pole, but it's reused across subtests in the same package). That's the price of confidence.

#### 12.8.5 Multi-instance LiveKit fan-out

Same pattern as the WS multi-instance test (§12.7), but for the room layer. Two backend instances share Postgres + Redis + the same LiveKit container. Alice connects WS to instance 1, Bob to instance 2. LiveKit webhook hits instance 1 → state mirrored in Redis → instance 2's hub picks up the pubsub → Bob receives the WS event.

This proves you can scale the backend horizontally without losing call events.

#### 12.8.6 What we are explicitly NOT testing at the backend layer

- **Actual media quality / codec negotiation** — LiveKit's job, not ours.
- **TURN traversal** — relevant for prod deploy, not v1 backend correctness.
- **Browser/Expo client UI** — handled by the frontend phase, downstream of this spec.
- **Recording / egress** — not in v1.

If the §12.8 matrix passes, you can confidently start the Expo app knowing the backend is the right shape.

---

## 13. CI/CD & tooling

### 13.1 `.golangci.yml` (golangci-lint v2 schema)

```yaml
version: "2"

run:
  timeout: 5m

linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck   # subsumes gosimple + unused in v2
    - bodyclose
    - errorlint
    - gocritic
    - misspell
    - revive
    - sqlclosecheck
    - rowserrcheck
  settings:
    errorlint:
      asserts: true
      comparison: true

formatters:
  enable:
    - gofmt
    - goimports
```

**v2 notes:** `gosimple` and `unused` are now subsumed into `staticcheck`, so they aren't listed separately. Formatters (`gofmt`, `goimports`) live under their own top-level `formatters:` block instead of `linters.enable`. Tests are linted by default — no `run.tests: true` flag needed.

### 13.2 `.conform.yaml` (mirror court-scraper)

```yaml
policies:
  - type: commit
    spec:
      conventional:
        types:
          - feat
          - fix
          - chore
          - refactor
          - test
          - docs
          - ci
          - perf
        scopes:
          - root
          - backend
          - mobile
          - infra
          - deps
      header:
        length: 89
      body:
        required: false
```

### 13.3 `lefthook.yml` (mirror court-scraper)

```yaml
pre-commit:
  parallel: true
  commands:
    fmt:
      glob: "*.go"
      run: gofmt -w {staged_files}                 # repo-relative paths; do NOT cd first
      stage_fixed: true
    lint:
      glob: "*.go"
      run: cd apps/backend && golangci-lint run ./...
    test:
      glob: "*.go"
      run: cd apps/backend && go test -race -count=1 ./...

commit-msg:
  commands:
    conform:
      run: conform enforce --commit-msg-file {1}
```

### 13.4 `justfile`

```just
set dotenv-load := true

# Local dev
dev:
    just up
    cd apps/backend && go run ./cmd/server

# Docker stack management
up:
    docker-compose up -d
    @echo "postgres on :5432  redis on :6379  minio on :9000  livekit on :7880"

down:
    docker-compose down

# Wipe everything — drops postgres data, minio buckets, redis state.
# Useful when migrations have diverged from the data, or for a fresh start.
clean:
    docker-compose down -v
    @echo "all containers + volumes removed. Run 'just up && just migrate-up' to rebuild."

# Tests
test:
    cd apps/backend && go test -race -count=1 ./...

test-cover:
    # Scoped to ./internal/... — the cmd/server main package has no tests and
    # triggers `go: no such tool "covdata"` on hosted runners when included with
    # -coverprofile. Lint + `go build ./...` still verify cmd/server compiles.
    # Restore broader scope at Phase 1.4 if cmd/server ever gets a test.
    cd apps/backend && go test -race -covermode=atomic -coverprofile=coverage.out ./internal/...

# Lint
lint:
    cd apps/backend && golangci-lint run ./...

# Migrations (goose)
migrate-up:
    goose -dir migrations postgres "$DATABASE_URL" up

migrate-down:
    goose -dir migrations postgres "$DATABASE_URL" down

migrate-status:
    goose -dir migrations postgres "$DATABASE_URL" status

migrate-create name:
    goose -dir migrations -s create {{name}} sql

# Swagger
gen-docs:
    cd apps/backend && swag init -g cmd/server/main.go -o ../../docs/openapi --parseDependency

# Generate mobile client from OpenAPI
gen-client:
    oapi-codegen -package wakeupapi -generate types,client docs/openapi/swagger.json > apps/mobile/lib/wakeupapi/client.go

# Verify all (used in CI and as the final acceptance gate)
verify: lint test gen-docs
    @echo "All checks passed."

# Reset local DB
db-reset:
    docker-compose down -v
    docker-compose up -d postgres
    sleep 2
    just migrate-up
```

### 13.5 GitHub Actions `ci.yml`

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  ci:
    runs-on: ubuntu-latest
    # No `services:` block — repository tests own their own infra via
    # testcontainers-go (postgres / redis / minio / livekit), which uses
    # the runner's host Docker. Adding GA services on top would just be a
    # second postgres on :5432 fighting testcontainers for the port.
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0   # conform validates the whole PR commit range, needs full history

      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
          cache: true                # caches GOMODCACHE + ~/.cache/go-build keyed on go.sum

      - uses: extractions/setup-just@v2

      # Cache Go-installed binaries (swag, goose) across runs.
      # Bump the cache key by editing this workflow when adding/removing a tool.
      - name: Cache Go-installed tools
        id: cache-go-bin
        uses: actions/cache@v4
        with:
          path: ~/go/bin
          key: go-bin-${{ runner.os }}-${{ hashFiles('.github/workflows/ci.yml') }}

      - name: Install tools (cache miss)
        if: steps.cache-go-bin.outputs.cache-hit != 'true'
        run: |
          go install github.com/swaggo/swag/cmd/swag@v1.16.4
          go install github.com/pressly/goose/v3/cmd/goose@latest

      - name: Lint
        uses: golangci/golangci-lint-action@v7
        with:
          version: v2.11.4
          working-directory: apps/backend

      - name: Test
        run: just test-cover

      - name: Generate docs
        run: just gen-docs

      - name: Conform
        uses: siderolabs/conform@v0.1.0-alpha.30
```

**Caching design notes:**
- `actions/setup-go` already caches GOMODCACHE + Go build cache keyed on `go.sum`, so dependency downloads + compilation cache restore for free.
- `extractions/setup-just` installs prebuilt `just` binary from a release tarball — much faster than `curl | bash` script install.
- `actions/cache` keyed on the workflow-file hash holds onto `~/go/bin` between runs, so `swag`/`goose` only build from source when this file changes.
- `golangci/golangci-lint-action` has its own internal binary cache and runs the linter in one step, replacing the manual `go install golangci-lint` + `just lint` pair.
- Tool versions are pinned to keep cache keys stable (`@latest` would resolve differently each day and bust the cache).

### 13.6 `.coderabbit.yaml`

```yaml
language: en-US
reviews:
  request_changes_workflow: true
  high_level_summary: true
  poem: false
  review_status: true
  collapse_walkthrough: false
  auto_review:
    enabled: true
    drafts: false
  path_instructions:
    - path: "apps/backend/**"
      instructions: |
        Enforce the layered architecture: handler → service → repository → storage.
        Repositories must use the DBTX interface, never *pgxpool.Pool directly.
        Every handler must have swaggo annotations.
        Every test must use t.Parallel() and pgtestdb for DB isolation.
        Errors returned to clients must be *apierror.Error.
    - path: "apps/backend/internal/{argon2id,session,apierror,pagination,ratelimit,pubsub,mailer,pushnotif,objectstore,wsproto}/**"
      instructions: |
        These are utility packages. They must not import from internal/repository, internal/service, internal/handler, or internal/middleware.
chat:
  auto_reply: true
```

### 13.7 The CodeRabbit feedback loop

Every PR triggers CodeRabbit review. The AI MUST:
1. Wait for CodeRabbit comments after pushing a commit.
2. Read every actionable comment (ignore poetry, summaries).
3. Address every comment with a `fix(scope): address CodeRabbit on <thing>` commit.
4. Push and wait for re-review.
5. Loop until CodeRabbit approves or has no actionable comments left.
6. Only then mark the milestone complete and proceed.

### 13.8 `.gitignore` (root) — exact contents

```gitignore
# Go build
*.exe
*.exe~
*.dll
*.so
*.dylib
*.test
*.out
/bin/
/dist/
/build/
vendor/
go.work
go.work.sum

# Env (NEVER commit real env)
.env
.env.local
.env.*.local

# IDE
.idea/
.vscode/
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# Coverage
coverage.out
coverage.html
*.coverage

# Logs
*.log

# Local data (docker-compose volumes)
.docker-data/
.minio-data/
.postgres-data/

# Expo / Node (mobile, even though we don't write JS yet)
node_modules/
.expo/
.expo-shared/
dist-mobile/
```

**Commit `.env.example` (with placeholder values), never `.env`.**

### 13.9 `.env.example` (root) — exact contents

```dotenv
# --- Backend ---
ENV=local                         # local | staging | production
LOG_LEVEL=debug                   # debug | info | warn | error
HTTP_ADDR=:8080
SESSION_DOMAIN=localhost

# --- Database ---
DATABASE_URL=postgres://wakeup:wakeup@localhost:5432/wakeup?sslmode=disable

# --- Redis ---
REDIS_URL=redis://localhost:6379/0

# --- Object storage (MinIO locally, S3 in prod) ---
S3_ENDPOINT=http://localhost:9000
S3_REGION=us-east-1
S3_ACCESS_KEY=minioadmin
S3_SECRET_KEY=minioadmin
S3_BUCKET=wakeup
S3_FORCE_PATH_STYLE=true          # required for MinIO

# --- Email (Resend) — operator must obtain ---
RESEND_API_KEY=
RESEND_FROM_EMAIL=no-reply@wakeup.app

# --- Voice & video (self-hosted LiveKit, runs in docker-compose) ---
# Defaults match the LIVEKIT_KEYS line in docker-compose.yml — no external account needed.
LIVEKIT_URL=ws://localhost:7880
LIVEKIT_API_KEY=devkey
LIVEKIT_API_SECRET=devsecretdevsecretdevsecret

# --- Push notifications (Expo) — operator must obtain after Expo project creation ---
EXPO_ACCESS_TOKEN=

# --- Error tracking (Sentry) — operator must obtain ---
SENTRY_DSN=
SENTRY_ENVIRONMENT=local

# --- CORS ---
CORS_ALLOWED_ORIGINS=http://localhost:8081,exp://localhost:8081
```

---

## 14. Operator setup (Baron — do this BEFORE handing the spec to the AI)

The AI cannot create cloud accounts, accept TOS, or paste secrets into `.env`. You (Baron) must complete **everything in this section** before the AI is unblocked. If the AI hits a missing credential, it will stop and ask — that costs you a round-trip.

### 14.1 Local tooling install

Install on your dev machine:

- [ ] Go 1.23+ — `brew install go` or official installer
- [ ] Docker Desktop (for docker-compose + testcontainers) — must be running before any local dev
- [ ] `just` — `brew install just`
- [ ] `lefthook` — `go install github.com/evilmartians/lefthook@latest`
- [ ] `conform` — `go install github.com/siderolabs/conform/cmd/conform@latest`
- [ ] `golangci-lint` — `brew install golangci-lint` (or official installer)
- [ ] `goose` — `go install github.com/pressly/goose/v3/cmd/goose@latest` (or `brew install goose`)
- [ ] `swag` — `go install github.com/swaggo/swag/cmd/swag@latest`
- [ ] `oapi-codegen` — `go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest`
- (LiveKit is part of the project's `docker-compose.yml` per §10.3 — you do NOT need to install it separately or sign up for LiveKit Cloud.)

### 14.2 GitHub repo + CodeRabbit

- [ ] Create the GitHub repository (private is fine).
- [ ] Push the initial empty commit on `main`.
- [ ] Install the **CodeRabbit GitHub App** on the repo: https://github.com/apps/coderabbitai → Configure → grant access to the wakeup repo.
- [ ] In CodeRabbit dashboard, confirm the repo is connected and auto-review is enabled.
- [ ] Branch protection on `main`: require CI passing + at least one CodeRabbit approval before merge.

### 14.3 External service accounts (capture every key into `.env`)

- [ ] **Sentry** — create project at https://sentry.io (free tier). Copy DSN → `SENTRY_DSN`.
- [ ] **Resend** — sign up at https://resend.com. Verify a sending domain (or use `onboarding@resend.dev` for dev). Generate API key → `RESEND_API_KEY`. Set `RESEND_FROM_EMAIL`.
- **LiveKit** — NO external account required. LiveKit runs as a service in our `docker-compose.yml` (see §10.3). The `.env.example` already has working dev defaults (`LIVEKIT_URL=ws://localhost:7880`, `LIVEKIT_API_KEY=devkey`, `LIVEKIT_API_SECRET=devsecretdevsecretdevsecret`). Production deploy of LiveKit is out of v1 scope; when ready, deploy the same Docker image to a Hetzner VPS with a real config file mounted (~$20/mo, 20 TB included bandwidth — see §10.3).
- [ ] **Expo** — create an Expo account at https://expo.dev. Generate an access token → `EXPO_ACCESS_TOKEN`. (Needed for sending push notifications via Expo Push API.)
- [ ] **Fly.io** (deferred to deploy time, not v1 build) — create app, set up secrets via `fly secrets set ...`. Plan to mirror `.env` contents.

### 14.4 Local env file

- [ ] Copy `.env.example` to `.env`.
- [ ] Paste every secret captured above into `.env`.
- [ ] Verify `.env` is gitignored (the spec's gitignore covers it; double-check after `git add .`).
- [ ] Run `just dev` once to confirm postgres + redis + minio start cleanly via docker-compose.

### 14.5 First-time CI check

- [ ] After the AI completes Phase 0, manually verify on GitHub that:
  - The CI workflow ran and passed
  - CodeRabbit posted a review (even if just "looks good")
  - Branch protection blocks merging without both
- [ ] If anything is misconfigured, fix it before letting the AI continue. The whole loop in §15 depends on these gates working.

### 14.6 Hand-off

Once 14.1–14.5 are checked, paste this into the AI's prompt:

> Read `docs/WAKEUP.md` in full. Confirm you understand §0 (rules), §4 (architecture + conventions), §13 (tooling), §15 (your workflow), and §16 (the checklist). Then begin at §16 Phase 0, milestone 0.1. Follow §15 strictly per milestone. If you are unsure about a library or API, query the web for the official docs (per §0 rule 8). Stop and ask me only if you hit a missing credential or an ambiguous spec. Otherwise, work end-to-end.

---

## 15. Workflow rules (per milestone)

For every checkbox in §16:

1. **Read the milestone description fully.**
2. **Implement the change** in the package(s) named.
3. **Write the tests** for that change in the same commit.
4. **Run locally**: `just verify` must pass.
5. **Commit** with the exact commit message specified.
6. **Open a PR** (or push to main if working trunk-based — confirm with operator).
7. **Wait for CI green.**
8. **Wait for CodeRabbit review.**
9. **Address CodeRabbit feedback** in follow-up commits until clean.
10. **Check off the box.** Move to the next milestone.

**If you are stuck or unsure how something works:**
- Re-read the relevant section of this spec.
- Query the web for the official docs of the library/API in question (per §0 rule 8). Use WebFetch on the official README, pkg.go.dev, or the library's docs site.
- Read the failing test output carefully — it almost always tells you the answer.
- If the spec is genuinely ambiguous or contradictory, **stop and ask the operator**. Do not guess.

Never skip a step. Never disable a check. Never `--no-verify`.

---

## 16. Sequential build checklist

> **Each commit message is mandatory and must be used verbatim. No paraphrasing.**

### Phase 0 — Bootstrap

- [ ] **0.1** Initialize git repo in `wakeup/`. Create monorepo skeleton from §2.
  - Commit: `chore(root): scaffold monorepo skeleton`
- [ ] **0.2** Add `.gitignore` per §13.8 (verbatim — covers Go, env, IDE, OS, coverage, docker volumes, and Expo). Add `LICENSE` (MIT or operator's choice). Add `README.md` (one-paragraph stub pointing to `docs/WAKEUP.md`). **Verify `.env` is ignored**: create a fake `.env` with `TEST=1`, run `git status`, confirm it does not appear, then delete the fake file.
  - Commit: `chore(root): add gitignore license and readme stub`
- [ ] **0.3** Add `.golangci.yml` per §13.1.
  - Commit: `ci(root): add golangci-lint config`
- [ ] **0.4** Add `.conform.yaml` per §13.2.
  - Commit: `ci(root): add conform commit-message config`
- [ ] **0.5** Add `lefthook.yml` per §13.3. Run `lefthook install` locally.
  - Commit: `ci(root): add lefthook pre-commit and commit-msg hooks`
- [ ] **0.6** Add `justfile` per §13.4 (recipes that don't exist yet are placeholders).
  - Commit: `chore(root): add justfile with dev/test/lint/migrate recipes`
- [ ] **0.7** Add `docker-compose.yml` with `postgres:16`, `minio/minio:latest`, `redis:7`, AND `livekit/livekit-server:latest` (per the snippet in §10.3). Bind to standard ports. Persist volumes for postgres + minio. Verify all four services come up healthy with `docker-compose up -d` then `docker-compose ps`.
  - Commit: `chore(infra): add docker-compose for postgres redis minio and livekit`
- [ ] **0.8** Add `.env.example` per §13.9 (verbatim — every variable, with placeholders for secrets the operator will fill). Do **not** create `.env` — that is the operator's job per §14.4.
  - Commit: `chore(root): add env example`
- [ ] **0.9** Add `.github/workflows/ci.yml` per §13.5.
  - Commit: `ci(root): add github actions workflow`
- [ ] **0.10** Add `.coderabbit.yaml` per §13.6. (The operator already installed the CodeRabbit GitHub App per §14.2 — confirm by checking that a no-op PR triggers a review.)
  - Commit: `ci(root): configure coderabbit reviews`
- [ ] **0.11** Initialize Go module: `cd apps/backend && go mod init github.com/<org>/wakeup/apps/backend`. Add minimal `cmd/server/main.go` that prints "wakeup" and exits.
  - Commit: `feat(backend): initialize go module with placeholder main`
- [ ] **0.12** Verify `just verify` passes. Verify CI green on a no-op PR.
  - Commit: `ci(root): verify pipeline green` (only if any tweaks needed; otherwise skip)

### Phase 1 — Foundations

- [ ] **1.1** Add `apps/backend/internal/storage/dbtx.go` defining the `DBTX` interface (§4.2). No tests — it's a type definition.
  - Commit: `feat(backend): add DBTX interface for transaction-aware repositories`
- [ ] **1.2** Add `apps/backend/internal/storage/pool.go` opening a `*pgxpool.Pool` from `DATABASE_URL`. Add health-check method.
  - Commit: `feat(backend): add pgx connection pool`
- [ ] **1.3** Add ALL migrations from §5.1 (`migrations/0001_init.sql` through `migrations/0012_notification_preferences.sql`), each as a single goose-format file with matching `-- +goose Up` and `-- +goose Down` blocks. Verify `set_updated_at()` trigger is attached to `users`, `conversations`, `presence_states`, and `notification_preferences`. Verify the `color_scheme` column on `users` has the CHECK constraint. Run `just migrate-up` against local docker-compose; verify with `\d+ users` that the trigger and CHECK exist; then run `just migrate-down` once to confirm the Down block of the last migration reverses cleanly, and re-run `just migrate-up`.
  - Commit: `feat(backend): add full schema migrations with updated_at trigger`
- [ ] **1.4** Add `internal/testutil/containers.go` (testcontainers postgres + redis + minio). Each returns a connection string. Reuse via `sync.Once`.
  - Commit: `test(backend): add testcontainers helpers for postgres redis minio`
- [ ] **1.5** Add `internal/testutil/db.go` integrating `pgtestdb`. Migrations applied to template once; each test gets a clone.
  - Commit: `test(backend): add pgtestdb integration for parallel test isolation`
- [ ] **1.6** Add `internal/config/config.go` using `koanf` (env first, then `.env`). Strongly typed `Config` struct. Test loading from env.
  - Commit: `feat(backend): add koanf-based typed config loader`
- [ ] **1.7** Add `internal/log/log.go` setting up `slog` JSON handler. Level from config.
  - Commit: `feat(backend): add slog json logger setup`
- [ ] **1.8** Add `internal/repository/idempotency/` — `Get(key, userID)`, `Put(key, userID, hash, status, body, ttl)`, `DeleteExpired()`. Tests cover: hit, miss, hash mismatch, TTL expiry, cascade delete on user delete. Tests assert that the trigger correctly sets `updated_at` on update of any table that uses it (use `users` as the proof).
  - Commit: `feat(backend): add idempotency key repository`
- [ ] **1.9** Add `internal/testutil/harness.go` per §12.6 (test server, fakes for mailer/pusher/objectstore, miniredis, pgtestdb wiring). Add `internal/testutil/fixtures/` with `MakeUser`, plus stubs for `MakeFriendship`, `MakeConversation`, `MakeMessage`, `MakeAttachment` (real implementations land in their respective phases as the aggregates exist). Tests for the harness itself: `New(t)` works in parallel, `AuthClient` returns a session-bearing client.
  - Commit: `test(backend): add testutil harness and fixtures package`

### Phase 2 — Core packages

For each package: write the package, write exhaustive tests (every error path), run `just verify`, commit.

- [ ] **2.1** `internal/apierror/` — typed errors + helpers per §4.4. Tests: HTTP status mapping for every code, JSON marshaling, wrapping, FieldError shape, validator.ValidationErrors conversion.
  - Commit: `feat(backend): add apierror with typed codes and response helpers`
- [ ] **2.2** `internal/argon2id/` — wrapper per §8.1. Tests: hash-then-verify roundtrip, wrong password, malformed hash, empty password rejection.
  - Commit: `feat(backend): add argon2id wrapper around alexedwards/argon2id`
- [ ] **2.3** `internal/session/` — wrapper per §8.2 (cookies only, no Bearer adapter). Tests against a testcontainers postgres: cookie set on login, cookie cleared on logout, expired session returns no user, tampered cookie returns no user.
  - Commit: `feat(backend): add scs-based cookie session manager`
- [ ] **2.4** `internal/pagination/` — cursor encode/decode, query-builder helpers. Tests: round-trip, malformed cursor, cursor for non-existent record.
  - Commit: `feat(backend): add cursor pagination helpers`
- [ ] **2.5** `internal/pubsub/` — `Broker` interface + Redis impl + in-process impl. Tests for both: publish/subscribe roundtrip, multiple subscribers, unsubscribe.
  - Commit: `feat(backend): add pubsub broker with redis and in-process implementations`
- [ ] **2.6** `internal/ratelimit/` — Redis token bucket. Tests: burst allowed, sustained limit, recovery, separate keys.
  - Commit: `feat(backend): add redis-backed token bucket rate limiter`
- [ ] **2.7** `internal/objectstore/` — S3 wrapper per §9 (interface in §9.0; key conventions §9.1; bucket baseline §9.4). Implements `Put` (using `aws-sdk-go-v2`'s `manager.Uploader` so streaming uploads work), `PresignGet` (binds `response-content-disposition` into the signed URL when caller passes it; for avatars pass empty string), `Delete`. Tests against testcontainers MinIO: put + presign-and-fetch round-trip with the original filename appearing in `Content-Disposition`, delete returns no-error on missing key (idempotent), put rejects writes that exceed the configured size cap.
  - Commit: `feat(backend): add s3-compatible objectstore wrapper`
- [ ] **2.8** `internal/mailer/` — Resend wrapper. Tests use a fake HTTP server (no live Resend in CI).
  - Commit: `feat(backend): add resend mailer wrapper for password resets`
- [ ] **2.9** `internal/pushnotif/` — Expo Push HTTP wrapper. Tests use a fake HTTP server.
  - Commit: `feat(backend): add expo push notification wrapper`
- [ ] **2.10** `internal/wsproto/` — envelope types, encode/decode, type-name registry. Tests: round-trip every event type, unknown type rejection.
  - Commit: `feat(backend): add websocket message envelope and event types`

### Phase 3 — Auth + Users domain

- [ ] **3.1** `internal/repository/user/` — full CRUD + search by username (trigram). queries.sql + models.go + repo.go + repo_test.go. Tests cover: create, get-by-id, get-by-username, get-by-email, update, soft-delete, search with q, search empty, search prefix match, pagination edges.
  - Commit: `feat(backend): add user repository with trigram search`
- [ ] **3.2** `internal/service/auth/` — Register, Login, Logout, LogoutAll, Me, RequestPasswordReset, ConfirmPasswordReset. Uses argon2id + session + mailer + user repo.
  - Commit: `feat(backend): add auth service with registration login and password reset`
- [ ] **3.3** `internal/service/user/` — UpdateProfile (display_name, avatar_url, color_scheme), UploadAvatar, SoftDeleteAccount.
  - Commit: `feat(backend): add user service with profile color-scheme and avatar`
- [ ] **3.4** `internal/repository/notificationpref/` — Get (auto-creates default row on miss), Patch (any subset of bool fields). Tests cover: default insert on first read, partial update preserves untouched fields, cascade delete with user.
  - Commit: `feat(backend): add notification preferences repository`
- [ ] **3.5** `internal/service/notificationpref/` — GetForUser, UpdateForUser. Defaults applied on first call.
  - Commit: `feat(backend): add notification preferences service`
- [ ] **3.6** `internal/handler/http/auth_handler.go` — every `/v1/auth/*` endpoint per §6.2. DTOs per §4.10. Full swaggo annotations + examples per §6.3.
  - Commit: `feat(backend): add auth http handlers with dtos and swagger annotations`
- [ ] **3.7** `internal/handler/http/user_handler.go` — every `/v1/users/*` endpoint INCLUDING `GET /v1/users/me/notifications` and `PATCH /v1/users/me/notifications`. DTOs per §4.10 (`UserResponse`, `MeResponse`, `UpdateMeRequest`, `NotificationPreferencesResponse`, `UpdateNotificationPreferencesRequest`). Full swaggo annotations + examples per §6.3. Handler tests per the §12.5 matrix using harness from §12.6.
  - Commit: `feat(backend): add user http handlers with dtos and swagger annotations`
- [ ] **3.8** `internal/middleware/` — auth middleware (loads session, attaches User to context), require-auth, require-admin, request-id, request logging, rate limit, recovery.
  - Commit: `feat(backend): add http middleware chain`
- [ ] **3.9** Wire router in `cmd/server/main.go`. Mount Swagger UI at `/v1/docs`. Verify `just gen-docs && just dev` and Swagger UI loads with **every endpoint pre-populated and runnable from "Try it out" with zero typing**.
  - Commit: `feat(backend): wire chi router with auth and user routes`
- [ ] **3.10** Smoke test via Swagger UI: register, login, get me, update profile (incl. color_scheme to dark), get/patch notifications, upload avatar, request reset, confirm reset, logout. All work without manual field entry.
  - Commit: `docs(backend): smoke-tested auth and user endpoints via swagger`

### Phase 4 — Friends domain

- [ ] **4.1** `internal/repository/friendship/` — full CRUD. Tests: every state transition, dual-direction lookup, block prevents request.
  - Commit: `feat(backend): add friendship repository`
- [ ] **4.2** `internal/service/friend/` — SendRequest, AcceptRequest, DeclineRequest, ListFriends, ListRequests, Unfriend, Block, Unblock.
  - Commit: `feat(backend): add friend service`
- [ ] **4.3** `internal/handler/http/friend_handler.go` — all `/v1/friends/*` endpoints with swaggo.
  - Commit: `feat(backend): add friend http handlers with swagger annotations`
- [ ] **4.4** Smoke via Swagger UI.
  - Commit: `docs(backend): smoke-tested friend endpoints via swagger`

### Phase 5 — Conversations domain

- [ ] **5.1** `internal/repository/conversation/` — conversations + members. Tests cover: create direct, create group with cap-25 enforced at repo level, add/remove members, list by user (paginated by last_message_at).
  - Commit: `feat(backend): add conversation repository with member management`
- [ ] **5.2** `internal/service/conversation/` — Create, Get, Update, Leave, AddMembers, RemoveMember, MarkRead.
  - Commit: `feat(backend): add conversation service`
- [ ] **5.3** `internal/handler/http/conversation_handler.go` — all `/v1/conversations/*` endpoints (without `/messages` subroutes yet) with swaggo.
  - Commit: `feat(backend): add conversation http handlers with swagger annotations`
- [ ] **5.4** Smoke via Swagger UI.
  - Commit: `docs(backend): smoke-tested conversation endpoints via swagger`

### Phase 6 — Messages domain

- [ ] **6.1** `internal/repository/message/` — full CRUD + tsvector search + reads + attachments link. Tests: every soft-delete behavior, pagination across conversation_id+created_at, q full-text, read receipts.
  - Commit: `feat(backend): add message repository with full-text search and reads`
- [ ] **6.2** `internal/service/message/` — Send (transactional with attachment link), Edit, Delete, List (with q), MarkRead. Updates `conversations.last_message_at`. Publishes events to pubsub.
  - Commit: `feat(backend): add message service with publish-on-send`
- [ ] **6.3** `internal/handler/http/message_handler.go` — all message endpoints with swaggo.
  - Commit: `feat(backend): add message http handlers with swagger annotations`
- [ ] **6.4** Smoke via Swagger UI.
  - Commit: `docs(backend): smoke-tested message endpoints via swagger`

### Phase 7 — Attachments

- [ ] **7.1** `internal/repository/attachment/` — Create, GetByID, ListOrphansOlderThan(ctx, cutoff time.Time) (for the orphan sweeper, §9.6), DeleteByIDs(ctx, ids []uuid.UUID), and **CallerCanRead(ctx, attachmentID, userID uuid.UUID) (bool, error)** which returns true iff there exists a `message_attachments` row whose message lives in a conversation the user is a member of, OR if the attachment has zero `message_attachments` rows AND `uploader_id == userID` (orphan-during-compose case). Tests cover every branch.
  - Commit: `feat(backend): add attachment repository`
- [ ] **7.2** `internal/service/attachment/` — `Upload` (server-side MIME detection on first 512 bytes, sanitize filename, generate UUID-keyed storage path per §9.1, write to objectstore using the **detected** MIME, persist row with detected MIME), `GetForCaller` (calls `CallerCanRead`; on false returns `apierror.NotFound` per §9.3 — never `Forbidden`), `Presign` (issues presigned GET with `response-content-disposition: attachment; filename="<sanitized>"`).
  - Commit: `feat(backend): add attachment service with mime detection and membership gate`
- [ ] **7.3** `internal/handler/http/attachment_handler.go` — `POST /v1/attachments` and `GET /v1/attachments/{id}` per §6.2. The POST handler MUST wrap the request body in `http.MaxBytesReader(w, r.Body, 50*1024*1024+1024)` *before* calling `r.ParseMultipartForm(10<<20)`. DTOs: `AttachmentResponse` matches the §9.7 shape (`id`, `url`, `expires_at`, `filename`, `content_type`, `size_bytes`). Full swaggo + examples per §6.3.
  - Commit: `feat(backend): add attachment http handlers with swagger annotations`
- [ ] **7.4** Add `internal/service/attachment/orphan_sweeper.go` implementing the §4.12 `Job` interface — `Name()="attachment-orphan-sweeper"`, `Interval()=1h`. Run() finds attachments older than 24h with no `message_attachments` row, deletes the S3 object then the DB row. Register on the runner in `cmd/server/main.go`. Tests cover: not deleted before 24h, deleted after, S3-failed-then-DB-not-deleted leaves the row for retry next tick.
  - Commit: `feat(backend): add attachment orphan sweeper`
- [ ] **7.5** Smoke via Swagger UI: upload a PDF labeled `image/png` → expect 422 (MIME-detection caught the lie); upload >50 MB → expect 413; upload normal PDF → expect 200; GET as uploader before linking → 200 with presigned URL; GET as non-member → 404; link to a message and GET as message-thread member → 200.
  - Commit: `docs(backend): smoke-tested attachment endpoints via swagger`

### Phase 8 — WebSocket realtime

- [ ] **8.1** `internal/handler/ws/hub.go` — Hub + Conn types, read/write pumps, per-user fan-out. Tests with `httptest.NewServer` + real WS dial.
  - Commit: `feat(backend): add websocket hub with per-user fan-out`
- [ ] **8.2** Wire pubsub: services publish events; hub subscribes to channels for connected users; broadcasts to local connections.
  - Commit: `feat(backend): wire pubsub between services and websocket hub`
- [ ] **8.3** `internal/handler/ws/ws_handler.go` — `/v1/ws` upgrade, auth, subscribe to user's channels, route inbound events (heartbeat, typing, presence.set).
  - Commit: `feat(backend): add websocket upgrade handler with auth and routing`
- [ ] **8.4** Implement the full §12.7 WebSocket test matrix. For every event in §7.2: fires-when-expected, does-NOT-fire-when-not-expected, payload-shape, multi-instance fan-out (via `testutil.NewMultiInstance(t, 2)`), idempotency. Plus the connection-lifecycle subtable (upgrade auth, heartbeat, debounce, slow-consumer kick, simultaneous connections, reconnect-no-replay). Add `scripts/ci/check-ws-tests.sh` per §12.7 to enforce coverage. **No realtime milestone is complete until every row of the matrix is green.**
  - Commit: `test(backend): add end-to-end websocket integration tests`

### Phase 9 — Presence engine

- [ ] **9.1** `internal/repository/presence/` — Get, UpsertHeartbeat, SetStatus.
  - Commit: `feat(backend): add presence repository`
- [ ] **9.2** `internal/service/presence/` — handle heartbeat, decay rules, manual override. Background goroutine sweeps stale presence (online→away after 5min, away→offline after 1hr) every 30s. Publishes `presence.update` on any state change.
  - Commit: `feat(backend): add presence service with decay rules`
- [ ] **9.3** `/v1/presence/*` HTTP endpoints + `/v1/widget/friends` endpoint with swaggo.
  - Commit: `feat(backend): add presence and widget http handlers`
- [ ] **9.4** Smoke + verify status transitions in real time via two browser tabs.
  - Commit: `docs(backend): smoke-tested presence transitions`

### Phase 10 — Voice & Video (self-hosted LiveKit)

- [ ] **10.1** Add `testutil.StartLiveKit(t, webhookURL)` per §12.3 + §12.8 — testcontainers-based LiveKit server, configured with webhook URL pointing at the test harness. Add `Harness.LiveKitClient(t, identity, token)` returning a `*lksdk.Room`. Tests for the helpers themselves: container boots, client connects with valid token, fails with invalid token.
  - Commit: `test(backend): add livekit testcontainer and client helper`
- [ ] **10.2** `internal/service/room/` — `Join(ctx, userID, conversationID, video bool)` issues a LiveKit JWT per §10.3 (microphone+camera publish allowed; `video` flag is a UI hint, not a permission). `Leave(ctx, userID, conversationID)` is best-effort cleanup. `GetParticipants(ctx, conversationID)` reads from Redis. Authorize all calls via the conversation member check from `service/conversation`. Implements §12.8.1 (token issuance) and §12.8.2 (authorization) test matrices completely.
  - Commit: `feat(backend): add room service with livekit token issuance`
- [ ] **10.3** `internal/handler/http/room_handler.go` — `POST /v1/conversations/{id}/room/join`, `POST /v1/conversations/{id}/room/leave`, `GET /v1/conversations/{id}/room`. DTOs per §4.10. Full swaggo + examples per §6.3. Handler tests per §12.5 matrix.
  - Commit: `feat(backend): add room http handlers with dtos and swagger annotations`
- [ ] **10.4** Add `internal/handler/http/livekit_webhook_handler.go` — `POST /webhooks/livekit`, signature-verified per §10.3. Add `/webhooks/livekit` to the no-auth allowlist in the router and to the idempotency middleware skip list. Handle: `room_started`, `participant_joined`, `participant_left`, `track_published` (camera), `track_unpublished` (camera), `room_finished`. Update Redis state and publish `room.*` WS events through `internal/pubsub`. Tests implement the full §12.8.3 webhook matrix using the synthesizer from `LiveKitTestEnv.Receiver`.
  - Commit: `feat(backend): add livekit webhook handler with signature verification`
- [ ] **10.5** Implement the §12.8.4 end-to-end integration test in `internal/testutil/integration/livekit_e2e_test.go`. Real LiveKit container, two backend users, both connect via `lksdk` client, walk the full room lifecycle (start → join → camera-on → leave → end). Asserts Redis state, WS broadcasts, push notifications. **This test is the gate that says "the backend works perfectly before we start the frontend."**
  - Commit: `test(backend): add end-to-end livekit integration test`
- [ ] **10.6** Implement §12.8.5 multi-instance LiveKit fan-out test using `testutil.NewMultiInstance(t, 2)` sharing one LiveKit container. Proves backend can scale horizontally without losing call events.
  - Commit: `test(backend): add multi-instance livekit fan-out test`
- [ ] **10.7** Wire push notifications: when `room.started` fires, fetch conversation members; for each offline member with `notification_preferences.calls = true`, call `notification.SendOfflinePush` with category `calls`. (The push service comes online in Phase 11 — leave a TODO here, completed in 11.5.)
  - Commit: `feat(backend): trigger room.started push notifications for offline members`
- [ ] **10.8** Manual smoke via Swagger UI + a LiveKit web client (https://meet.livekit.io pointed at `ws://localhost:7880` with the issued token): two browser tabs as different users join the same conversation room. Verify both audio works, both can toggle camera, presence shows correctly in `GET .../room`, and `room.participant_joined` event fires. This complements the automated §12.8.4 test — it confirms the backend behaves correctly with a real third-party client too.
  - Commit: `docs(backend): smoke-tested voice and video room flow with real client`
  - Commit: `docs(backend): smoke-tested voice and video room flow`

### Phase 11 — Push notifications

- [ ] **11.1** `internal/repository/devicetoken/` — Register, Delete, ListByUser.
  - Commit: `feat(backend): add device token repository`
- [ ] **11.2** Extend `internal/service/notificationpref/` (created in 3.5) with a `ShouldNotify(userID, category)` method returning bool. Categories: `direct_messages`, `group_messages`, `friend_requests`, `calls`. Default true if no row.
  - Commit: `feat(backend): add ShouldNotify check to notification preferences service`
- [ ] **11.3** `internal/service/notification/` — `SendOfflinePush(ctx, recipientID, category, payload)`. Checks `ShouldNotify(recipientID, category)` first; if false, no-op. Otherwise fetches device tokens and pushes via `pushnotif`. Tests: pref off → no push, pref on → push, no devices → no error.
  - Commit: `feat(backend): add notification service for offline push gated by preferences`
- [ ] **11.4** `/v1/devices` endpoints with DTOs and swaggo annotations + examples per §6.3.
  - Commit: `feat(backend): add device token http handlers`
- [ ] **11.5** Wire into `MessageService.Send`: if recipient has no live WS connection (check Redis presence), call `notification.SendOfflinePush` with category `direct_messages` or `group_messages` based on conversation type. Wire similarly into `FriendService.SendRequest` (`friend_requests`) and `CallService.InitiateCall` (`calls`).
  - Commit: `feat(backend): trigger expo push for offline events with category routing`

### Phase 12 — Admin

- [ ] **12.1** `internal/repository/audit/` — Create, List (paginated).
  - Commit: `feat(backend): add audit log repository`
- [ ] **12.2** `internal/service/admin/` — ListUsers, GetUser, UpdateUser, ListAudit, StartImpersonation, EndImpersonation. Every admin action writes to audit_log; impersonation actions write `impersonate.started` / `impersonate.ended` bookend entries with `metadata.impersonating_user_id`. Enforces all §8.7 restrictions (cannot impersonate self/admin/deleted/missing).
  - Commit: `feat(backend): add admin service with impersonation and audit logging`
- [ ] **12.3** Update auth middleware (originally added in Phase 3.8) to read `impersonating_user_id` from the scs session and populate both `ctx.User` (effective) and `ctx.RealUser` (session owner). When the field is absent, both equal the session owner. Add tests asserting both values are correct in both states.
  - Commit: `feat(backend): add impersonation-aware auth middleware`
- [ ] **12.4** Add the impersonation guard middleware applied to `/v1/auth/password-reset/*`, `/v1/auth/logout-all`, `/v1/users/me` DELETE, and `/v1/users/me/notifications` PATCH. Returns 403 `BLOCKED_DURING_IMPERSONATION` when `ctx.User != ctx.RealUser`.
  - Commit: `feat(backend): block dangerous routes during impersonation`
- [ ] **12.5** `internal/handler/http/admin_handler.go` — every `/v1/admin/*` endpoint INCLUDING `POST /v1/admin/users/{id}/impersonate` and `POST /v1/admin/impersonate/end`. DTOs per §4.10 (extend `MeResponse` with `ImpersonatedBy`). Update `GET /v1/auth/me` (Phase 3) to populate `impersonated_by` when applicable. Full swaggo + examples per §6.3. Handler tests per the §12.5 matrix in the §8.7 test list.
  - Commit: `feat(backend): add admin http handlers with impersonation`
- [ ] **12.6** Smoke: promote a user to admin via SQL, log in as admin, list users via Swagger UI, impersonate another user, verify `/v1/auth/me` returns the target with `impersonated_by` set, attempt to change password (expect 403), end impersonation, verify `/v1/auth/me` returns admin again, check audit log entries.
  - Commit: `docs(backend): smoke-tested admin and impersonation endpoints`

### Phase 13 — Final hardening

- [ ] **13.1** Wire Sentry in `main.go`, panic-recovery middleware sends to Sentry.
  - Commit: `feat(backend): wire sentry error tracking`
- [ ] **13.2** Add `/v1/healthz` and `/v1/readyz`. `readyz` checks db + redis.
  - Commit: `feat(backend): add health and readiness endpoints`
- [ ] **13.3** Apply rate limit middleware to every route group per §8.3.
  - Commit: `feat(backend): apply rate limit middleware to all routes`
- [ ] **13.4** Wire idempotency middleware per §4.8 onto every POST/PATCH/PUT route. Start the background expired-key sweeper (every 1 hour). Verify with the matrix in §12.5 idempotency rows.
  - Commit: `feat(backend): wire idempotency middleware on all write routes`
- [ ] **13.5** Apply CORS + security headers per §8.4 and §8.5.
  - Commit: `feat(backend): add cors and security headers middleware`
- [ ] **13.6** Add `scripts/ci/check-handler-tests.sh` per §12.5 — parses each handler's swaggo `@Failure` annotations and asserts matching subtests exist in `_test.go`. Wire into the CI workflow as a required step.
  - Commit: `ci(backend): enforce handler test coverage matches swagger failures`
- [ ] **13.7** Run full test suite under `go test -race -count=10 ./...` — must pass.
  - Commit: `chore(backend): verify race-free behavior under repeated runs`
- [ ] **13.8** Run coverage: `just test-cover` — every package ≥80%. Add tests anywhere below.
  - Commit: `test(backend): raise coverage to ≥80% in <packages-touched>`
- [ ] **13.9** Generate final OpenAPI: `just gen-docs`. Verify Swagger UI loads, every endpoint is listed, every endpoint has a description, and every documented `@Failure` is reachable from the handler.
  - Commit: `docs(backend): regenerate openapi spec`
- [ ] **13.10** Generate mobile client stub: `just gen-client`. Commit the generated client.
  - Commit: `feat(mobile): generate api client from openapi spec`
- [ ] **13.11** Write a manual smoke-test script: register two users, become friends, create a group, send messages (one with an `Idempotency-Key`, retried, verify `Idempotent-Replay: true`), upload an attachment, verify presence updates, both users join the group's voice room (one toggles camera mid-call). Document as `docs/smoke.md`.
  - Commit: `docs(backend): add manual smoke test playbook`
- [ ] **13.12** Final `just verify` — green. Final CodeRabbit review — clean.
  - Commit: `chore(root): backend v1 ready for mobile`

---

## 17. Done criteria

The backend is **done** when, and only when, all of the following are true:

1. ✅ Every checkbox in §16 is checked (and §14 was checked off by the operator first).
2. ✅ `just verify` exits 0.
3. ✅ `go test -race -count=10 ./...` exits 0.
4. ✅ Coverage report shows every package ≥80%.
5. ✅ CI is green on `main`.
6. ✅ CodeRabbit has no open actionable comments.
7. ✅ Swagger UI at `/v1/docs` loads. Every endpoint has a description, request schema, response schema, and example response.
8. ✅ A human can register, log in, add a friend, create a group, send a message, upload an attachment, see presence update in real time, and have two users join the same conversation's voice room (both audio and one with video toggled on) — all via Swagger UI + a LiveKit test client. No bugs, no surprises.
9. ✅ The §12.7 WebSocket test matrix is fully green — every event has all five required subtests (fires, doesn't-fire, payload, multi-instance, idempotency) plus the connection-lifecycle subtable.
10. ✅ The §12.8 voice/video matrix is fully green — token issuance, authorization, webhook handling, end-to-end LiveKit integration test (12.8.4), and multi-instance fan-out (12.8.5) all pass with a real LiveKit container.
11. ✅ The mobile client stub at `apps/mobile/lib/wakeupapi/` compiles.

Until all 11 are true, the backend is not done. Do not begin Phase 14 (mobile/Expo) until the operator has personally verified the Swagger smoke test and the §12.8.4 LiveKit end-to-end test and signed off. The §12.7 + §12.8 matrices are the **specific guarantee** that the realtime + voice/video backend is correct; they replace the equivalent reassurance you'd otherwise only get from a working frontend.

---

## 18. Notes for the operator (you, handing this to the AI)

- **First**, complete every item in §14 (Operator Setup) yourself. The AI cannot create accounts or paste secrets.
- Hand this entire document to the AI. Tell it: **"Read WAKEUP.md fully. Then start at §16 Phase 0. Follow the rules in §0 and §15 strictly. Do not improvise."**
- After every phase, manually verify the commits, the CI run, and the CodeRabbit thread. Do not let the AI mark a checkbox without your confirmation.
- If the AI gets stuck, the answer is almost always: re-read the relevant section of this doc, check the workflow rules, look at the test failures, address them honestly. It is not: invent a workaround.
- Keep this document under version control in the repo at `docs/WAKEUP.md`. If the spec changes, the change is a `docs(root):` commit and the AI re-reads.

— end of spec —
