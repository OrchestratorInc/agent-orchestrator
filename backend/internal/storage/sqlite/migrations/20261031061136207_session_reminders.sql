-- Summary: persist one-shot follow-ups an agent schedules into its own session (ao remind).
-- +goose Up
-- +goose StatementBegin
CREATE TABLE session_reminders (
    id            TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name          TEXT NOT NULL DEFAULT '' CHECK (length(name) <= 120),
    message       TEXT NOT NULL CHECK (length(trim(message)) > 0 AND length(message) <= 4000),
    due_at        TIMESTAMP NOT NULL,
    -- Who scheduled it: 'agent' (ao remind from inside the session) or 'user'.
    created_by    TEXT NOT NULL DEFAULT 'agent' CHECK (created_by IN ('agent', 'user')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'cancelled', 'dropped')),
    -- Why a reminder was dropped, or the last delivery error while pending.
    status_reason TEXT NOT NULL DEFAULT '',
    attempts      INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    resolved_at   TIMESTAMP,
    CHECK ((status = 'pending' AND resolved_at IS NULL) OR (status <> 'pending' AND resolved_at IS NOT NULL))
);

CREATE INDEX idx_session_reminders_due
    ON session_reminders(status, due_at, id);
CREATE INDEX idx_session_reminders_session
    ON session_reminders(session_id, status, due_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_session_reminders_session;
DROP INDEX IF EXISTS idx_session_reminders_due;
DROP TABLE IF EXISTS session_reminders;
-- +goose StatementEnd
