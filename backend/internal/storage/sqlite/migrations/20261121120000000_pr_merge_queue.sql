-- +goose Up
-- Whether the PR's base branch merges through a merge queue, and the PR's
-- place in it as JSON (state, position, estimate, enqueued time); '' while the
-- PR isn't queued. Rows from before these columns read as no queue until the
-- observer's next fetch of them.
ALTER TABLE pr ADD COLUMN merge_queue_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE pr ADD COLUMN merge_queue_entry TEXT NOT NULL DEFAULT '';

-- A PR moving through the queue changes none of the facts pr_cdc_update
-- watched, so the views listening for pr_updated would not see it join, move
-- up, or leave the queue. The rest of the trigger is 0111's, unchanged.
DROP TRIGGER IF EXISTS pr_cdc_update;

-- +goose StatementBegin
CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
    OR OLD.merge_queue_required <> NEW.merge_queue_required
    OR OLD.merge_queue_entry <> NEW.merge_queue_entry
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
                    'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS pr_cdc_update;

-- +goose StatementBegin
CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
                    'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd

ALTER TABLE pr DROP COLUMN merge_queue_entry;
ALTER TABLE pr DROP COLUMN merge_queue_required;
