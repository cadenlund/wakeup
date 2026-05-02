-- +goose Up
CREATE TABLE presence_states (
    user_id           uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status            text NOT NULL DEFAULT 'offline' CHECK (status IN ('online','away','offline','sleeping')),
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
