-- +goose Up
CREATE TABLE ao_user_sandbox_preferences (
    user_id UUID PRIMARY KEY REFERENCES ao_users(id) ON DELETE CASCADE,
    sandbox_provider TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE ao_user_sandbox_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_user_sandbox_preferences FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_user_sandbox_preferences_owner_policy
    ON ao_user_sandbox_preferences
    USING (user_id = ao_current_user_id())
    WITH CHECK (user_id = ao_current_user_id());

-- +goose Down
DROP TABLE ao_user_sandbox_preferences;
