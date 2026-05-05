set dotenv-load := true

# Local dev — bring the full stack up (containers wait-for-healthy,
# bucket bootstrapped) and start the backend in this terminal.
dev:
    just up
    just bootstrap
    cd apps/backend && go run ./cmd/server

# Docker stack management. `--wait` blocks until every service's
# healthcheck passes, so callers can chain `migrate-up` / `bootstrap`
# without racing postgres on cold start.
up:
    docker-compose up -d --wait
    @echo "postgres on :5432  redis on :6379  minio on :9000  livekit on :7880"

down:
    docker-compose down

# Wipe everything — containers, named volumes, AND the bind-mounted
# data dirs under .docker-data/. Postgres + minio both bind-mount,
# which means a bare `compose down -v` left their state on disk and
# the next `up` reused stale credentials / bucket layout. The
# .docker-data wipe is what makes "fresh checkout" semantics actual.
# Sweeps stragglers + the network too in case a previous failed `up`
# left a half-created postgres container behind without its port
# mapping (the gremlin that ate an hour on 2026-05-05).
clean:
    docker-compose down -v --remove-orphans
    rm -rf .docker-data/postgres .docker-data/minio
    @echo "containers + volumes + .docker-data/ wiped. Run 'just dev-reset' to rebuild."

# Idempotent local-stack bootstrap: creates the MinIO `wakeup` bucket
# the backend's avatar / attachment uploads write into. Safe to re-run
# (`--ignore-existing` is a no-op when the bucket is already there).
# Bucket name is hardcoded to match S3_BUCKET in .env — if you change
# one, change both.
bootstrap:
    docker exec wakeup-minio-1 mc alias set local http://localhost:9000 minioadmin minioadmin >/dev/null
    docker exec wakeup-minio-1 mc mb --ignore-existing local/wakeup

# Full nuke-and-pave: clean + up + migrate + bootstrap. Use after a
# schema change that doesn't migrate cleanly, after a credential
# rotation in .env, or just when something is wrong and you want to
# start over without thinking. Doesn't start the backend — chain
# `&& just dev` if you want the whole flow.
dev-reset: clean up migrate-up bootstrap
    @echo "stack rebuilt clean. 'just dev' to start the backend."

# Tests
test:
    cd apps/backend && go test -race -count=1 ./...

# Coverage recipe — produces apps/backend/coverage.out. Scoped to ./internal/...
# because the cmd/server main package has no tests and triggers a `go: no such tool
# "covdata"` error on hosted runners when included with -coverprofile (see GH actions
# logs from PR #1). Restore broader scope at Phase 1.4 if/when cmd/server gets tests.
test-cover:
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

# Swagger — output lives INSIDE apps/backend so cmd/server can import the
# generated package (Go modules can't import packages outside their root).
# A second copy at docs/openapi/ stays generated for tools and the mobile
# client codegen step below.
gen-docs:
    cd apps/backend && swag init -g cmd/server/main.go -o internal/docs/openapi --parseDependency
    mkdir -p docs/openapi
    cp apps/backend/internal/docs/openapi/swagger.json docs/openapi/swagger.json
    cp apps/backend/internal/docs/openapi/swagger.yaml docs/openapi/swagger.yaml

# Verifies the committed swagger artifacts match what gen-docs produces.
# CI runs this after gen-docs so a forgotten regen fails the pipeline
# instead of silently shipping a stale spec.
gen-docs-check: gen-docs
    @if ! git diff --quiet HEAD -- apps/backend/internal/docs/openapi/docs.go docs/openapi/swagger.json docs/openapi/swagger.yaml; then \
        echo "ERROR: swagger artifacts are stale — run 'just gen-docs' and commit the output."; \
        git --no-pager diff HEAD -- apps/backend/internal/docs/openapi/docs.go docs/openapi/swagger.json docs/openapi/swagger.yaml | head -80; \
        exit 1; \
    fi

# Generate mobile client TYPES from OpenAPI. The Expo client consumes
# the API via TypeScript, so we emit a typed schema via
# openapi-typescript and let the data-fetching layer (TanStack Query
# hooks, etc.) be layered on in the Expo phase.
#
# swag emits Swagger 2.0 and openapi-typescript only reads OpenAPI 3.x,
# so we pipe through the same v2→v3 converter at scripts/dev/. The
# converter handles the v2→v3 subset we actually use: parameter
# type → schema.type, formData → multipart requestBody,
# definitions → components.schemas.
#
# Depends on `gen-docs` so we never feed a stale swagger.json into the
# converter. CLI is pinned (vs `@latest`) so output is reproducible
# across machines and CI; bump it deliberately. (CodeRabbit on PR #97.)
gen-client: gen-docs
    #!/usr/bin/env bash
    set -euo pipefail
    # The converted OpenAPI 3 file is committed alongside the
    # Swagger 2 source so downstream tools (Orval for hooks,
    # openapi-typescript for the schema, future SDKs) all read from
    # one canonical artifact instead of each running their own
    # conversion.
    mkdir -p apps/mobile/lib/api docs/openapi
    python3 scripts/dev/swagger2-to-openapi3.py docs/openapi/swagger.json docs/openapi/openapi.json
    npx -y openapi-typescript@7.4.4 docs/openapi/openapi.json -o apps/mobile/lib/api/schema.ts
    cd apps/mobile && bunx orval --config ./lib/api/orval.config.ts

# Verify all (used in CI and as the final acceptance gate)
verify: lint test gen-docs-check
    @echo "All checks passed."

# Mobile dev server, tunnel mode. The QR code that prints is the
# canonical artifact for the per-screen review gate (WAKEUPEXPO §12.5):
# scan it with Expo Go on the operator's phone before any
# screen-bearing milestone is checked off. Tunnel routes through
# ngrok-style relay so the phone reaches Metro from any network
# (cell data, separate Wi-Fi, etc.), not just LAN.
mobile-tunnel:
    cd apps/mobile && bunx expo start --tunnel

# Mobile dev server, LAN-only. Faster start than --tunnel; requires
# the phone and laptop to be on the same network. Use when tunnel
# is being slow or unavailable.
mobile-dev:
    cd apps/mobile && bunx expo start

# Type-check + lint the mobile package. Wired into CI per
# milestone 0.8. Test step lands in Phase 2 once the API client is
# in place — until then there's nothing of substance to test.
mobile-verify:
    cd apps/mobile && bunx tsc --noEmit
    cd apps/mobile && bunx eslint . --max-warnings 0

# Reset local DB. Postgres data is bind-mounted to ./.docker-data/postgres
# (not a Docker-managed volume), so `docker-compose down -v` alone leaves
# the data directory intact and goose reports "no migrations to run" on
# next up. The rm -rf wipes the bind mount; the trailing `|| true`
# tolerates the dir not existing on a fresh checkout.
db-reset:
    docker-compose down -v
    rm -rf ./.docker-data/postgres || true
    docker-compose up -d postgres
    sleep 2
    just migrate-up
