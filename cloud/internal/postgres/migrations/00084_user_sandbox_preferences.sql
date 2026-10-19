-- +goose Up
-- This branch-only migration was previously numbered 00040. Local databases
-- may already have its table and policy; retain those objects and their data.
CREATE TABLE IF NOT EXISTS ao_user_sandbox_preferences (
    user_id UUID PRIMARY KEY REFERENCES ao_users(id) ON DELETE CASCADE,
    sandbox_provider TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE ao_user_sandbox_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_user_sandbox_preferences FORCE ROW LEVEL SECURITY;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policy
        WHERE polrelid = 'ao_user_sandbox_preferences'::regclass
            AND polname = 'ao_user_sandbox_preferences_owner_policy'
    ) THEN
        CREATE POLICY ao_user_sandbox_preferences_owner_policy
            ON ao_user_sandbox_preferences
            USING (user_id = ao_current_user_id())
            WITH CHECK (user_id = ao_current_user_id());
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE ao_user_sandbox_preferences;
