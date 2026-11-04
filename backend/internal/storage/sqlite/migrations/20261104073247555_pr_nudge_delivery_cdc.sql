-- +goose Up
-- Review delivery changes the derived Kanban phrase even when the worker's
-- activity and the observed PR facts have not changed.
-- +goose StatementBegin
CREATE TRIGGER pr_nudge_delivery_cdc_update
AFTER UPDATE OF last_nudge_signature ON pr
WHEN OLD.last_nudge_signature IS NOT NEW.last_nudge_signature
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS pr_nudge_delivery_cdc_update;
-- +goose StatementEnd
