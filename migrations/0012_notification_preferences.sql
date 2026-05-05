-- Per-user preferences. Originally just push-notification toggles
-- (hence the table name); now also holds the user's chosen sleep-cycle
-- color scheme and the light/dark mode override (the §4.5 token system
-- on the mobile client). A row is auto-created with defaults the first
-- time the user is fetched. Push delivery (Phase 11) checks the
-- notification flags; the mobile theme provider checks the theme
-- columns on session start so the user's pick follows them across
-- devices.

-- +goose Up
CREATE TABLE notification_preferences (
    user_id               uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    direct_messages       boolean NOT NULL DEFAULT true,
    group_messages        boolean NOT NULL DEFAULT true,
    friend_requests       boolean NOT NULL DEFAULT true,
    calls                 boolean NOT NULL DEFAULT true,
    -- Theme picker (mobile §4.5): scheme + mode-override on independent
    -- axes. `theme_scheme = 'system'` means "let the client resolve a
    -- default" (typically daylight in light mode, midnight in dark);
    -- `theme_mode_preference = 'system'` means "follow OS Appearance".
    theme_scheme          text NOT NULL DEFAULT 'system' CHECK (theme_scheme IN (
        'system','sunrise','daylight','noon','golden','meadow',
        'dusk','twilight','aurora','midnight','rem')),
    theme_mode_preference text NOT NULL DEFAULT 'system' CHECK (theme_mode_preference IN (
        'system','light','dark')),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER notification_preferences_set_updated_at
    BEFORE UPDATE ON notification_preferences
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS notification_preferences_set_updated_at ON notification_preferences;
DROP TABLE IF EXISTS notification_preferences;
