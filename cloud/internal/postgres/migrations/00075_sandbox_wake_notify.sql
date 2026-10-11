-- +goose Up

-- Wake the reconciler the moment a sandbox is asked to run again (a resume, a
-- message or review to a paused session, a restore, waking idle sessions), from
-- whichever path asked. Without it the reconciler only notices at its next
-- tick, which can lag seconds behind while that tick refreshes every live
-- sandbox. The tick stays the correctness fallback for a lost notification.
-- +goose StatementBegin
CREATE FUNCTION ao_notify_sandbox_wake() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.desired_state = 'running'
        AND (TG_OP = 'INSERT' OR OLD.desired_state IS DISTINCT FROM 'running') THEN
        PERFORM pg_notify('ao_sandbox_wake', NEW.session_id::text);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER ao_sandboxes_wake_notify
    AFTER INSERT OR UPDATE OF desired_state
    ON ao_sandboxes
    FOR EACH ROW EXECUTE FUNCTION ao_notify_sandbox_wake();

-- +goose Down
DROP TRIGGER IF EXISTS ao_sandboxes_wake_notify ON ao_sandboxes;
DROP FUNCTION IF EXISTS ao_notify_sandbox_wake();
