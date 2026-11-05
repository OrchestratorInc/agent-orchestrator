-- +goose Up
ALTER TABLE review_run ADD COLUMN publication_json TEXT NOT NULL DEFAULT 'null' CHECK (json_valid(publication_json));
-- +goose StatementBegin
DROP TRIGGER review_run_cdc_insert;
DROP TRIGGER review_run_cdc_update;
CREATE TRIGGER review_run_cdc_insert
AFTER INSERT ON review_run
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'review_run_created',
        json_object(
            'id', NEW.id,
            'reviewId', NEW.review_id,
            'sessionId', NEW.session_id,
            'pr', NEW.pr_url,
            'targetSha', NEW.target_sha,
            'status', NEW.status,
            'verdict', NEW.verdict,
            'triggerSource', NEW.trigger_source,
            'githubReviewId', NEW.github_review_id,
            'publication', json(NEW.publication_json),
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END)
        ),
        NEW.created_at);
END;

CREATE TRIGGER review_run_cdc_update
AFTER UPDATE ON review_run
WHEN OLD.status <> NEW.status
    OR OLD.verdict <> NEW.verdict
    OR OLD.body <> NEW.body
    OR OLD.github_review_id <> NEW.github_review_id
    OR OLD.publication_json <> NEW.publication_json
    OR OLD.auto_inject_review <> NEW.auto_inject_review
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'review_run_updated',
        json_object(
            'id', NEW.id,
            'reviewId', NEW.review_id,
            'sessionId', NEW.session_id,
            'pr', NEW.pr_url,
            'targetSha', NEW.target_sha,
            'status', NEW.status,
            'verdict', NEW.verdict,
            'triggerSource', NEW.trigger_source,
            'githubReviewId', NEW.github_review_id,
            'publication', json(NEW.publication_json),
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END)
        ),
        datetime('now'));
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER review_run_cdc_insert;
DROP TRIGGER review_run_cdc_update;
CREATE TRIGGER review_run_cdc_insert
AFTER INSERT ON review_run
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'review_run_created',
        json_object(
            'id', NEW.id,
            'reviewId', NEW.review_id,
            'sessionId', NEW.session_id,
            'pr', NEW.pr_url,
            'targetSha', NEW.target_sha,
            'status', NEW.status,
            'verdict', NEW.verdict,
            'triggerSource', NEW.trigger_source,
            'githubReviewId', NEW.github_review_id,
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END)
        ),
        NEW.created_at);
END;

CREATE TRIGGER review_run_cdc_update
AFTER UPDATE ON review_run
WHEN OLD.status <> NEW.status
    OR OLD.verdict <> NEW.verdict
    OR OLD.body <> NEW.body
    OR OLD.github_review_id <> NEW.github_review_id
    OR OLD.auto_inject_review <> NEW.auto_inject_review
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'review_run_updated',
        json_object(
            'id', NEW.id,
            'reviewId', NEW.review_id,
            'sessionId', NEW.session_id,
            'pr', NEW.pr_url,
            'targetSha', NEW.target_sha,
            'status', NEW.status,
            'verdict', NEW.verdict,
            'triggerSource', NEW.trigger_source,
            'githubReviewId', NEW.github_review_id,
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END)
        ),
        datetime('now'));
END;
-- +goose StatementEnd
ALTER TABLE review_run DROP COLUMN publication_json;
