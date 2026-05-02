-- Per-user toggles for push notification categories. A row is auto-created
-- with defaults the first time the user is fetched. Push delivery (Phase 11)
-- checks the relevant flag before sending.

-- +goose Up
CREATE TABLE notification_preferences (
    user_id         uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    direct_messages boolean NOT NULL DEFAULT true,
    group_messages  boolean NOT NULL DEFAULT true,
    friend_requests boolean NOT NULL DEFAULT true,
    calls           boolean NOT NULL DEFAULT true,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER notification_preferences_set_updated_at
    BEFORE UPDATE ON notification_preferences
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS notification_preferences_set_updated_at ON notification_preferences;
DROP TABLE IF EXISTS notification_preferences;
