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
    username        citext NOT NULL UNIQUE,
    display_name    text NOT NULL,
    email           citext NOT NULL UNIQUE,
    password_hash   text NOT NULL,
    avatar_url      text,
    bio             text CHECK (char_length(bio) <= 280),
    status_emoji    text CHECK (char_length(status_emoji) <= 8),
    color_scheme    text NOT NULL DEFAULT 'system' CHECK (color_scheme IN ('light','dark','system')),
    role            text NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
    -- Per-account onboarding-completed timestamp. NULL = first
    -- post-login experience hasn't run yet; the mobile carousel
    -- (WAKEUPEXPO §3.0) reads this off /v1/auth/me and routes the
    -- user into (onboarding) until they finish + the
    -- POST /v1/users/me/onboarding/complete endpoint stamps a
    -- value here. Server-side flag (vs AsyncStorage) so signing
    -- in on a new device doesn't re-onboard the same user.
    onboarded_at    timestamptz,
    -- Stored-generated SHA-256 of the lowercased email. Used by
    -- POST /v1/contacts/match (WAKEUP.md §6.2): the client hashes its
    -- address book and asks "which of these belong to existing accounts"
    -- without ever sending raw addresses. Generated column means the
    -- application never has to remember to recompute it on email change.
    -- pgcrypto's digest() is enabled at the top of this file.
    email_hash      bytea GENERATED ALWAYS AS (digest(lower(email::text), 'sha256')) STORED,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE INDEX users_username_trgm_idx ON users USING gin (username gin_trgm_ops);
CREATE INDEX users_display_name_trgm_idx ON users USING gin (display_name gin_trgm_ops);
CREATE INDEX users_active_idx ON users (id) WHERE deleted_at IS NULL;
CREATE INDEX users_email_hash_idx ON users (email_hash) WHERE deleted_at IS NULL;

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS users_set_updated_at ON users;
DROP INDEX IF EXISTS users_email_hash_idx;
DROP INDEX IF EXISTS users_active_idx;
DROP INDEX IF EXISTS users_display_name_trgm_idx;
DROP INDEX IF EXISTS users_username_trgm_idx;
DROP TABLE IF EXISTS users;
DROP FUNCTION IF EXISTS set_updated_at();
DROP EXTENSION IF EXISTS citext;
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS pgcrypto;
