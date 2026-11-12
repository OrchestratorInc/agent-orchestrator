-- +goose Up
-- A session's related pull requests: PRs it reviews or contributes to without
-- owning them. Display data only: ownership, polling, CI and review routing,
-- and lifecycle stay on the pr table, and nothing reads this table to act on a
-- PR. url is the canonical PR/MR web URL; title, state and author are what the
-- provider said when the link was made.
CREATE TABLE session_pr_links (
    session_id TEXT      NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    url        TEXT      NOT NULL,
    provider   TEXT      NOT NULL,
    host       TEXT      NOT NULL,
    repository TEXT      NOT NULL,
    number     INTEGER   NOT NULL,
    relation   TEXT      NOT NULL CHECK (relation IN ('reviewing', 'contributing')),
    title      TEXT      NOT NULL DEFAULT '',
    state      TEXT      NOT NULL DEFAULT '',
    author     TEXT      NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (session_id, url)
);

-- +goose StatementBegin
CREATE TRIGGER session_pr_links_cdc_insert
AFTER INSERT ON session_pr_links
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT sessions.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id), CURRENT_TIMESTAMP
    FROM sessions WHERE sessions.id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER session_pr_links_cdc_update
AFTER UPDATE ON session_pr_links
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT sessions.project_id, NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id), CURRENT_TIMESTAMP
    FROM sessions WHERE sessions.id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER session_pr_links_cdc_delete
AFTER DELETE ON session_pr_links
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT sessions.project_id, OLD.session_id, 'session_updated',
        json_object('id', OLD.session_id), CURRENT_TIMESTAMP
    FROM sessions WHERE sessions.id = OLD.session_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS session_pr_links_cdc_delete;
DROP TRIGGER IF EXISTS session_pr_links_cdc_update;
DROP TRIGGER IF EXISTS session_pr_links_cdc_insert;
DROP TABLE IF EXISTS session_pr_links;
