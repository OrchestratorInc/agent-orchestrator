-- +goose Up

-- Freestyle is a sandbox provider. Extend the provider check constraint with
-- it rather than replacing the constraint with a fixed list: an environment
-- may run a constraint that already allows other providers (staging once ran
-- a build that added one), and a fixed list would reject those rows and fail
-- this migration. A missing constraint, or one already allowing freestyle,
-- is left as is.
-- +goose StatementBegin
DO $$
DECLARE
    current_def TEXT;
    new_def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid) INTO current_def
    FROM pg_constraint c
    WHERE c.conrelid = 'ao_sandboxes'::regclass
      AND c.conname = 'ao_sandboxes_provider_check';
    IF current_def IS NULL OR current_def LIKE '%''freestyle''%' THEN
        RETURN;
    END IF;
    -- pg_get_constraintdef renders IN (...) as = ANY (ARRAY['a'::text, ...]).
    new_def := replace(current_def, 'ARRAY[', 'ARRAY[''freestyle''::text, ');
    IF new_def NOT LIKE '%''freestyle''%' THEN
        RAISE EXCEPTION 'unexpected ao_sandboxes_provider_check definition: %', current_def;
    END IF;
    ALTER TABLE ao_sandboxes DROP CONSTRAINT ao_sandboxes_provider_check;
    EXECUTE 'ALTER TABLE ao_sandboxes ADD CONSTRAINT ao_sandboxes_provider_check ' || new_def;
END
$$;
-- +goose StatementEnd

-- +goose Down

-- A Freestyle sandbox cannot be represented once freestyle is removed: the
-- re-added constraint rejects such rows and the rollback fails instead of
-- silently relabelling live provider resources.
-- +goose StatementBegin
DO $$
DECLARE
    current_def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid) INTO current_def
    FROM pg_constraint c
    WHERE c.conrelid = 'ao_sandboxes'::regclass
      AND c.conname = 'ao_sandboxes_provider_check';
    IF current_def IS NULL OR current_def NOT LIKE '%''freestyle''%' THEN
        RETURN;
    END IF;
    ALTER TABLE ao_sandboxes DROP CONSTRAINT ao_sandboxes_provider_check;
    EXECUTE 'ALTER TABLE ao_sandboxes ADD CONSTRAINT ao_sandboxes_provider_check '
        || replace(replace(current_def, '''freestyle''::text, ', ''), ', ''freestyle''::text', '');
END
$$;
-- +goose StatementEnd
