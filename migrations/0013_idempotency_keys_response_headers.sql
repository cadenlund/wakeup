-- Adds response_headers to idempotency_keys so the §4.8 cache replay
-- preserves Content-Type and any other custom headers handlers set on
-- write responses. Without this column, replays would lose Content-Type
-- and clients would parse a JSON body as text/plain.
--
-- Stored as jsonb of {"Header": ["v1", "v2"]} so multi-value headers
-- (e.g. Set-Cookie) round-trip via http.Header's []string semantics.

-- +goose Up
ALTER TABLE idempotency_keys
ADD COLUMN response_headers jsonb;

-- +goose Down
ALTER TABLE idempotency_keys DROP COLUMN IF EXISTS response_headers;
