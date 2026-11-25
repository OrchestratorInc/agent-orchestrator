-- +goose Up
-- +goose StatementBegin
-- A Chat composer stages a picked file before the message that names it is
-- sent, so the bytes survive navigation and restart. Each such file is leased
-- to its unsent draft here. The row is removed the moment any accepted user
-- message names the file (the triggers below run inside the insert or edit
-- that accepts it), so a file referenced by history has no lease and is never
-- reclaimed. A lease that is released (its chip removed or its draft
-- discarded) or that expires without renewal is reclaimed: both stored copies
-- are deleted, then the row. Files staged without a lease (spawn briefs,
-- terminal sends, browser annotations, everything staged before this table)
-- are never reclaimed before their session is deleted.
--
-- Times are Unix milliseconds. Names are 128-bit random per staged file, so a
-- name appearing anywhere in a message is a reference to that file.
CREATE TABLE chat_attachment_leases (
    session_id     TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    created_at_ms  INTEGER NOT NULL,
    renewed_at_ms  INTEGER NOT NULL,
    released_at_ms INTEGER,
    PRIMARY KEY (session_id, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_insert
AFTER INSERT ON conversation_messages
WHEN NEW.role = 'user'
BEGIN
    DELETE FROM chat_attachment_leases
    WHERE released_at_ms IS NULL
      AND (instr(NEW.text, name) > 0 OR instr(NEW.delivery_content_json, name) > 0);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_update
AFTER UPDATE OF text, delivery_content_json ON conversation_messages
WHEN NEW.role = 'user'
BEGIN
    DELETE FROM chat_attachment_leases
    WHERE released_at_ms IS NULL
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
    DELETE FROM chat_attachment_leases
    WHERE released_at_ms IS NULL
      AND instr(NEW.request_json, name) > 0;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_edit
AFTER INSERT ON conversation_edit_deliveries
BEGIN
    DELETE FROM chat_attachment_leases
    WHERE released_at_ms IS NULL
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
