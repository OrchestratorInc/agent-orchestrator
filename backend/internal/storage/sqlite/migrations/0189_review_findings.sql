-- +goose Up
-- AO's own reviewer files findings here instead of as inline PR comments
-- (#6300). The worker receives them through AO and resolves them in AO, so
-- they never become provider review threads the SCM comment watcher would
-- re-deliver. Rows are history: a resolution or supersession only moves the
-- status forward, never rewrites the finding.
CREATE TABLE review_finding (
    id                     TEXT PRIMARY KEY,
    run_id                 TEXT NOT NULL REFERENCES review_run (id) ON DELETE CASCADE,
    session_id             TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    pr_url                 TEXT NOT NULL DEFAULT '',
    target_sha             TEXT NOT NULL DEFAULT '',
    ordinal                INTEGER NOT NULL,
    path                   TEXT NOT NULL DEFAULT '',
    line                   INTEGER NOT NULL DEFAULT 0,
    body                   TEXT NOT NULL,
    status                 TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'resolved', 'superseded')),
    resolution_note        TEXT NOT NULL DEFAULT '',
    -- The AO session that resolved the finding. Empty means a person did, from
    -- the app or a terminal outside any agent session. Provider logins are
    -- never recorded here: AO's reviewer and worker can share one account.
    resolved_by_session_id TEXT NOT NULL DEFAULT '',
    resolved_at            TIMESTAMP,
    superseded_by_run_id   TEXT NOT NULL DEFAULT '',
    created_at             TIMESTAMP NOT NULL,
    UNIQUE (run_id, ordinal)
);
CREATE INDEX idx_review_finding_session ON review_finding (session_id, pr_url, status);

-- A finding change is a change to its run as far as clients are concerned:
-- they already refresh a session's reviews on review_run_updated.
-- +goose StatementBegin
CREATE TRIGGER review_finding_cdc_update
AFTER UPDATE ON review_finding
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, r.session_id, 'review_run_updated',
        json_object(
            'id', r.id,
            'reviewId', r.review_id,
            'sessionId', r.session_id,
            'pr', r.pr_url,
            'targetSha', r.target_sha,
            'status', r.status,
            'verdict', r.verdict,
            'triggerSource', r.trigger_source,
            'githubReviewId', r.github_review_id,
            'autoInjectReview', json(CASE WHEN r.auto_inject_review THEN 'true' ELSE 'false' END),
            'findingId', NEW.id,
            'findingStatus', NEW.status
        ),
        datetime('now')
    FROM review_run r JOIN sessions s ON s.id = r.session_id
    WHERE r.id = NEW.run_id;
END;
-- +goose StatementEnd

-- The daemon posts the pass's summary to the provider; a failed post is kept
-- visible on the run instead of being retried behind the user's back.
ALTER TABLE review_run ADD COLUMN provider_post_error TEXT NOT NULL DEFAULT '';

-- A reply AO's own provider identity wrote on an existing review thread: the
-- worker answering feedback. It is never review work for that worker (#5574).
ALTER TABLE pr_comment ADD COLUMN own_reply BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE pr_comment DROP COLUMN own_reply;
ALTER TABLE review_run DROP COLUMN provider_post_error;
DROP TRIGGER IF EXISTS review_finding_cdc_update;
DROP INDEX IF EXISTS idx_review_finding_session;
DROP TABLE IF EXISTS review_finding;
