-- +goose Up
-- `status` is the *effective* presence — what other users see. The WS hub
-- writes 'online' on connect, the decay sweeper demotes to 'away' / 'offline',
-- and the user's manual override can flip it to anything in the enum.
--
-- `intent` is the *sticky* override. When non-null, the WS hub leaves
-- `status` alone — the manual value sticks across app backgrounding /
-- WS disconnect / decay cycles. Without intent, DND (the most common
-- case) would clear the moment the user locks their phone, defeating
-- the point. Both columns share the same enum minus 'offline' for intent
-- (you can't manually mark yourself offline; that's what logout is).
CREATE TABLE presence_states (
    user_id           uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status            text NOT NULL DEFAULT 'offline' CHECK (status IN ('online','away','offline','sleeping','dnd')),
    intent            text CHECK (intent IN ('online','away','sleeping','dnd')),
    last_active_at    timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER presence_states_set_updated_at
    BEFORE UPDATE ON presence_states
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS presence_states_set_updated_at ON presence_states;
DROP TABLE IF EXISTS presence_states;
