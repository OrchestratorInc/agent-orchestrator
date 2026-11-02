-- +goose Up
CREATE TABLE telemetry_lifetime_sessions (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    count INTEGER NOT NULL CHECK (count >= 0)
);
INSERT INTO telemetry_lifetime_sessions (singleton, count)
SELECT 1, COUNT(*) FROM sessions WHERE is_task_preparation = 0;
-- +goose StatementBegin
CREATE TRIGGER telemetry_lifetime_sessions_insert AFTER INSERT ON sessions
WHEN NEW.is_task_preparation = 0
BEGIN
    UPDATE telemetry_lifetime_sessions SET count = count + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER telemetry_lifetime_sessions_promote AFTER UPDATE OF is_task_preparation ON sessions
WHEN OLD.is_task_preparation = 1 AND NEW.is_task_preparation = 0
BEGIN
    UPDATE telemetry_lifetime_sessions SET count = count + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER telemetry_lifetime_sessions_promote;
DROP TRIGGER telemetry_lifetime_sessions_insert;
DROP TABLE telemetry_lifetime_sessions;
