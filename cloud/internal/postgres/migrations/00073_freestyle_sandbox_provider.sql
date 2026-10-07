-- +goose Up

-- Freestyle is a sandbox provider. Keep the database constraint in sync with
-- the provider registry so session creation can persist its sandbox row before
-- the reconciler boots the Freestyle VM.
ALTER TABLE ao_sandboxes
    DROP CONSTRAINT IF EXISTS ao_sandboxes_provider_check;

ALTER TABLE ao_sandboxes
    ADD CONSTRAINT ao_sandboxes_provider_check
    CHECK (provider IN ('ecs', 'daytona', 'docker', 'nodeops', 'coder', 'freestyle'));

-- +goose Down

-- A Freestyle sandbox cannot be represented by the prior schema. Fail the
-- rollback instead of silently relabelling live provider resources.
ALTER TABLE ao_sandboxes
    DROP CONSTRAINT IF EXISTS ao_sandboxes_provider_check;

ALTER TABLE ao_sandboxes
    ADD CONSTRAINT ao_sandboxes_provider_check
    CHECK (provider IN ('ecs', 'daytona', 'docker', 'nodeops', 'coder'));
