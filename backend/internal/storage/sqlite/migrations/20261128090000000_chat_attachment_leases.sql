-- +goose Up
-- +goose StatementBegin
-- A Chat composer stages a picked file before the message that names it is
-- sent, so the bytes survive navigation and restart. Each such file is leased
-- to its unsent draft here. Files staged without a lease (spawn briefs,
-- terminal sends, browser annotations, everything staged before this table)
-- are never reclaimed before their session is deleted.
--
-- Lifecycle of a row:
--   live       released_at_ms IS NULL
--   released   released_at_ms set: the draft gave the file up, or it expired
--   reclaiming reclaiming_at_ms set: reclamation claimed it and is deleting
--              both stored copies
--   reclaimed  reclaimed_at_ms set: both copies are gone; the row stays as a
--              tombstone so a late reference is refused, and is pruned later
--
-- Accepting a message that names a file (the triggers below run inside the
-- write that accepts it) deletes a live or released row, which commits the
-- file for good: a release that lost the race to acceptance is undone. Once
-- reclamation has claimed a row, acceptance is refused instead, so an
-- accepted message can never name a file that is being or has been deleted.
--
-- A side question is recorded outside the conversation tables, so it commits
-- in two steps: it holds its files (accepting counts the holds in flight)
-- before it is recorded, deletes the row once it is, and drops only its own
-- hold if it is not. Reclamation never claims a held row, and any other
-- acceptance deletes the row outright, so dropping a hold never re-leases a
-- file another message accepted.
--
-- Times are Unix milliseconds. Names are 128-bit random per staged file, so a
-- name appearing anywhere in a message is a reference to that file.
CREATE TABLE chat_attachment_leases (
    session_id       TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    created_at_ms    INTEGER NOT NULL,
    renewed_at_ms    INTEGER NOT NULL,
    released_at_ms   INTEGER,
    reclaiming_at_ms INTEGER,
    reclaimed_at_ms  INTEGER,
    accepting        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_insert
AFTER INSERT ON conversation_messages
WHEN NEW.role = 'user'
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE reclaiming_at_ms IS NOT NULL
          AND (instr(NEW.text, name) > 0 OR instr(NEW.delivery_content_json, name) > 0)
    );
    DELETE FROM chat_attachment_leases
    WHERE reclaiming_at_ms IS NULL
      AND (instr(NEW.text, name) > 0 OR instr(NEW.delivery_content_json, name) > 0);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_update
AFTER UPDATE OF text, delivery_content_json ON conversation_messages
WHEN NEW.role = 'user'
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE reclaiming_at_ms IS NOT NULL
          AND (instr(NEW.text, name) > 0 OR instr(NEW.delivery_content_json, name) > 0)
          AND instr(OLD.text, name) = 0 AND instr(OLD.delivery_content_json, name) = 0
    );
    DELETE FROM chat_attachment_leases
    WHERE reclaiming_at_ms IS NULL
      AND (instr(NEW.text, name) > 0 OR instr(NEW.delivery_content_json, name) > 0);
END;
-- +goose StatementEnd

-- Steering and edits of sent messages reserve their request before any
-- message row exists. Committing at reservation keeps the file on every path
-- that could still deliver it.
-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_steer
AFTER INSERT ON conversation_steer_deliveries
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE reclaiming_at_ms IS NOT NULL AND instr(NEW.request_json, name) > 0
    );
    DELETE FROM chat_attachment_leases
    WHERE reclaiming_at_ms IS NULL
      AND instr(NEW.request_json, name) > 0;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_edit
AFTER INSERT ON conversation_edit_deliveries
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE reclaiming_at_ms IS NOT NULL AND instr(NEW.request_json, name) > 0
    );
    DELETE FROM chat_attachment_leases
    WHERE reclaiming_at_ms IS NULL
      AND instr(NEW.request_json, name) > 0;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_edit;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_steer;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_update;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_insert;
DROP TABLE IF EXISTS chat_attachment_leases;
-- +goose StatementEnd
