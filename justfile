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
    cd apps/backend && go test -race -cover ./...

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
