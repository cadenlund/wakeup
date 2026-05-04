-- +goose Up
CREATE TABLE device_tokens (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expo_token   text NOT NULL CHECK (length(trim(expo_token)) > 0),
    platform     text NOT NULL CHECK (platform IN ('ios','android')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, expo_token)
);

-- iOS PushKit VoIP tokens are a separate transport from Expo push:
-- they wake the app from a fully-killed state, are delivered via
-- Apple's PushKit not APNS, and need a different APNS topic suffix.
-- Stored in their own table so the existing device_tokens code path
-- isn't disturbed and the VoIP-specific lifecycle (single token per
-- user, opt-in, etc.) stays clean. iOS-only — Android uses a
-- high-priority FCM data message via the existing expo path.
CREATE TABLE voip_tokens (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    voip_token   text NOT NULL CHECK (length(trim(voip_token)) > 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, voip_token)
);

-- +goose Down
DROP TABLE IF EXISTS voip_tokens;
DROP TABLE IF EXISTS device_tokens;
