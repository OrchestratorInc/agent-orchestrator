-- +goose Up
-- A chat message can send an image by its staged path: the daemon reads the
-- file, sends its bytes, and records the path as the image block's uri. The
-- commit triggers (chat_attachment_leases) match a staged file's name anywhere
-- in a message, across sessions. For an image block's uri two things change:
--
--   * It counts only for the session that owns the conversation (its session,
--     or an orchestrator conversation's current one), the session the daemon
--     read the file from. A fork copies its parent's files under the same
--     names, and its own send must neither commit nor be refused for the
--     parent's lease.
--   * It is refused when reclamation has claimed that file, even after the
--     file is gone. The read comes before the accepting write; if the draft
--     released the file and reclamation deleted it in between, history would
--     name a file that is gone and a retry would fail on it before its receipt.
--
-- Text, and every other content block, keep the original matching exactly.
-- The JSON functions are only reached for a name that appears and for valid
-- JSON, so a message that names no staged file costs one instr per lease.
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_insert;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_update;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_steer;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_edit;

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_insert
AFTER INSERT ON conversation_messages
WHEN NEW.role = 'user'
BEGIN
    -- Refused: a file reclamation is deleting, named anywhere (as before), or
    -- one it claimed, named as this session's image even once it is gone.
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE chat_attachment_leases.reclaiming_at_ms IS NOT NULL
          AND ((chat_attachment_leases.reclaimed_at_ms IS NULL AND (instr(NEW.text, chat_attachment_leases.name) > 0 OR CASE
                                                      WHEN instr(NEW.delivery_content_json, chat_attachment_leases.name) = 0 THEN 0
                                                      WHEN NOT json_valid(NEW.delivery_content_json) THEN 1
                                                      ELSE EXISTS (SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
                                                          WHERE instr(block.value, chat_attachment_leases.name) > 0
                                                            AND (block.type IS NOT 'object' OR json_extract(block.value, '$.type') IS NOT 'image')) END))
               OR (CASE
                      WHEN instr(NEW.delivery_content_json, chat_attachment_leases.name) = 0 THEN 0
                      WHEN NOT json_valid(NEW.delivery_content_json) THEN 0
                      ELSE (EXISTS (SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
                          WHERE block.type = 'object'
                            AND json_extract(block.value, '$.type') = 'image'
                            AND json_extract(block.value, '$.uri') = '.ao/attachments/' || chat_attachment_leases.name)
                        AND chat_attachment_leases.session_id IN (
                            SELECT session_id FROM conversations WHERE id = NEW.conversation_id
                            UNION SELECT current_session_id FROM conversations WHERE id = NEW.conversation_id)) END))
    );
    -- Committed: a live or released file the message names.
    DELETE FROM chat_attachment_leases
    WHERE chat_attachment_leases.reclaiming_at_ms IS NULL
      AND ((instr(NEW.text, chat_attachment_leases.name) > 0 OR CASE
               WHEN instr(NEW.delivery_content_json, chat_attachment_leases.name) = 0 THEN 0
               WHEN NOT json_valid(NEW.delivery_content_json) THEN 1
               ELSE EXISTS (SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
                   WHERE instr(block.value, chat_attachment_leases.name) > 0
                     AND (block.type IS NOT 'object' OR json_extract(block.value, '$.type') IS NOT 'image')) END)
           OR (CASE
                  WHEN instr(NEW.delivery_content_json, chat_attachment_leases.name) = 0 THEN 0
                  WHEN NOT json_valid(NEW.delivery_content_json) THEN 0
                  ELSE (EXISTS (SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
                      WHERE block.type = 'object'
                        AND json_extract(block.value, '$.type') = 'image'
                        AND json_extract(block.value, '$.uri') = '.ao/attachments/' || chat_attachment_leases.name)
                    AND chat_attachment_leases.session_id IN (
                        SELECT session_id FROM conversations WHERE id = NEW.conversation_id
                        UNION SELECT current_session_id FROM conversations WHERE id = NEW.conversation_id)) END));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_update
AFTER UPDATE OF text, delivery_content_json ON conversation_messages
WHEN NEW.role = 'user'
BEGIN
    -- Refused: a file reclamation is deleting, named anywhere (as before), or
    -- one it claimed, named as this session's image even once it is gone.
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE chat_attachment_leases.reclaiming_at_ms IS NOT NULL
          AND instr(OLD.text, chat_attachment_leases.name) = 0 AND instr(OLD.delivery_content_json, chat_attachment_leases.name) = 0
          AND ((chat_attachment_leases.reclaimed_at_ms IS NULL AND (instr(NEW.text, chat_attachment_leases.name) > 0 OR CASE
                                                      WHEN instr(NEW.delivery_content_json, chat_attachment_leases.name) = 0 THEN 0
                                                      WHEN NOT json_valid(NEW.delivery_content_json) THEN 1
                                                      ELSE EXISTS (SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
                                                          WHERE instr(block.value, chat_attachment_leases.name) > 0
                                                            AND (block.type IS NOT 'object' OR json_extract(block.value, '$.type') IS NOT 'image')) END))
               OR (CASE
                      WHEN instr(NEW.delivery_content_json, chat_attachment_leases.name) = 0 THEN 0
                      WHEN NOT json_valid(NEW.delivery_content_json) THEN 0
                      ELSE (EXISTS (SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
                          WHERE block.type = 'object'
                            AND json_extract(block.value, '$.type') = 'image'
                            AND json_extract(block.value, '$.uri') = '.ao/attachments/' || chat_attachment_leases.name)
                        AND chat_attachment_leases.session_id IN (
                            SELECT session_id FROM conversations WHERE id = NEW.conversation_id
                            UNION SELECT current_session_id FROM conversations WHERE id = NEW.conversation_id)) END))
    );
    -- Committed: a live or released file the message names.
    DELETE FROM chat_attachment_leases
    WHERE chat_attachment_leases.reclaiming_at_ms IS NULL
      AND ((instr(NEW.text, chat_attachment_leases.name) > 0 OR CASE
               WHEN instr(NEW.delivery_content_json, chat_attachment_leases.name) = 0 THEN 0
               WHEN NOT json_valid(NEW.delivery_content_json) THEN 1
               ELSE EXISTS (SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
                   WHERE instr(block.value, chat_attachment_leases.name) > 0
                     AND (block.type IS NOT 'object' OR json_extract(block.value, '$.type') IS NOT 'image')) END)
           OR (CASE
                  WHEN instr(NEW.delivery_content_json, chat_attachment_leases.name) = 0 THEN 0
                  WHEN NOT json_valid(NEW.delivery_content_json) THEN 0
                  ELSE (EXISTS (SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
                      WHERE block.type = 'object'
                        AND json_extract(block.value, '$.type') = 'image'
                        AND json_extract(block.value, '$.uri') = '.ao/attachments/' || chat_attachment_leases.name)
                    AND chat_attachment_leases.session_id IN (
                        SELECT session_id FROM conversations WHERE id = NEW.conversation_id
                        UNION SELECT current_session_id FROM conversations WHERE id = NEW.conversation_id)) END));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_steer
AFTER INSERT ON conversation_steer_deliveries
BEGIN
    -- Refused: a file reclamation is deleting, named anywhere (as before), or
    -- one it claimed, named as this session's image even once it is gone.
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE chat_attachment_leases.reclaiming_at_ms IS NOT NULL
          AND ((chat_attachment_leases.reclaimed_at_ms IS NULL AND (CASE
                                                      WHEN instr(NEW.request_json, chat_attachment_leases.name) = 0 THEN 0
                                                      WHEN NOT json_valid(NEW.request_json) THEN 1
                                                      WHEN json_type(NEW.request_json) IS NOT 'object' THEN 1
                                                      WHEN instr(json_remove(NEW.request_json, '$.content'), chat_attachment_leases.name) > 0 THEN 1
                                                      WHEN json_type(NEW.request_json, '$.content') IS NOT 'array' THEN instr(json_extract(NEW.request_json, '$.content'), chat_attachment_leases.name) > 0
                                                      ELSE EXISTS (SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
                                                          WHERE instr(block.value, chat_attachment_leases.name) > 0
                                                            AND (block.type IS NOT 'object' OR json_extract(block.value, '$.type') IS NOT 'image')) END))
               OR (CASE
                      WHEN instr(NEW.request_json, chat_attachment_leases.name) = 0 THEN 0
                      WHEN NOT json_valid(NEW.request_json) THEN 0
                      WHEN json_type(NEW.request_json) IS NOT 'object' THEN 0
                      WHEN json_type(NEW.request_json, '$.content') IS NOT 'array' THEN 0
                      ELSE (EXISTS (SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
                          WHERE block.type = 'object'
                            AND json_extract(block.value, '$.type') = 'image'
                            AND json_extract(block.value, '$.uri') = '.ao/attachments/' || chat_attachment_leases.name)
                        AND chat_attachment_leases.session_id IN (
                            SELECT session_id FROM conversations WHERE id = NEW.conversation_id
                            UNION SELECT current_session_id FROM conversations WHERE id = NEW.conversation_id)) END))
    );
    -- Committed: a live or released file the message names.
    DELETE FROM chat_attachment_leases
    WHERE chat_attachment_leases.reclaiming_at_ms IS NULL
      AND ((CASE
               WHEN instr(NEW.request_json, chat_attachment_leases.name) = 0 THEN 0
               WHEN NOT json_valid(NEW.request_json) THEN 1
               WHEN json_type(NEW.request_json) IS NOT 'object' THEN 1
               WHEN instr(json_remove(NEW.request_json, '$.content'), chat_attachment_leases.name) > 0 THEN 1
               WHEN json_type(NEW.request_json, '$.content') IS NOT 'array' THEN instr(json_extract(NEW.request_json, '$.content'), chat_attachment_leases.name) > 0
               ELSE EXISTS (SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
                   WHERE instr(block.value, chat_attachment_leases.name) > 0
                     AND (block.type IS NOT 'object' OR json_extract(block.value, '$.type') IS NOT 'image')) END)
           OR (CASE
                  WHEN instr(NEW.request_json, chat_attachment_leases.name) = 0 THEN 0
                  WHEN NOT json_valid(NEW.request_json) THEN 0
                  WHEN json_type(NEW.request_json) IS NOT 'object' THEN 0
                  WHEN json_type(NEW.request_json, '$.content') IS NOT 'array' THEN 0
                  ELSE (EXISTS (SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
                      WHERE block.type = 'object'
                        AND json_extract(block.value, '$.type') = 'image'
                        AND json_extract(block.value, '$.uri') = '.ao/attachments/' || chat_attachment_leases.name)
                    AND chat_attachment_leases.session_id IN (
                        SELECT session_id FROM conversations WHERE id = NEW.conversation_id
                        UNION SELECT current_session_id FROM conversations WHERE id = NEW.conversation_id)) END));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_edit
AFTER INSERT ON conversation_edit_deliveries
BEGIN
    -- Refused: a file reclamation is deleting, named anywhere (as before), or
    -- one it claimed, named as this session's image even once it is gone.
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE chat_attachment_leases.reclaiming_at_ms IS NOT NULL
          AND ((chat_attachment_leases.reclaimed_at_ms IS NULL AND (CASE
                                                      WHEN instr(NEW.request_json, chat_attachment_leases.name) = 0 THEN 0
                                                      WHEN NOT json_valid(NEW.request_json) THEN 1
                                                      WHEN json_type(NEW.request_json) IS NOT 'object' THEN 1
                                                      WHEN instr(json_remove(NEW.request_json, '$.content'), chat_attachment_leases.name) > 0 THEN 1
                                                      WHEN json_type(NEW.request_json, '$.content') IS NOT 'array' THEN instr(json_extract(NEW.request_json, '$.content'), chat_attachment_leases.name) > 0
                                                      ELSE EXISTS (SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
                                                          WHERE instr(block.value, chat_attachment_leases.name) > 0
                                                            AND (block.type IS NOT 'object' OR json_extract(block.value, '$.type') IS NOT 'image')) END))
               OR (CASE
                      WHEN instr(NEW.request_json, chat_attachment_leases.name) = 0 THEN 0
                      WHEN NOT json_valid(NEW.request_json) THEN 0
                      WHEN json_type(NEW.request_json) IS NOT 'object' THEN 0
                      WHEN json_type(NEW.request_json, '$.content') IS NOT 'array' THEN 0
                      ELSE (EXISTS (SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
                          WHERE block.type = 'object'
                            AND json_extract(block.value, '$.type') = 'image'
                            AND json_extract(block.value, '$.uri') = '.ao/attachments/' || chat_attachment_leases.name)
                        AND chat_attachment_leases.session_id IN (
                            SELECT session_id FROM conversations WHERE id = NEW.conversation_id
                            UNION SELECT current_session_id FROM conversations WHERE id = NEW.conversation_id)) END))
    );
    -- Committed: a live or released file the message names.
    DELETE FROM chat_attachment_leases
    WHERE chat_attachment_leases.reclaiming_at_ms IS NULL
      AND ((CASE
               WHEN instr(NEW.request_json, chat_attachment_leases.name) = 0 THEN 0
               WHEN NOT json_valid(NEW.request_json) THEN 1
               WHEN json_type(NEW.request_json) IS NOT 'object' THEN 1
               WHEN instr(json_remove(NEW.request_json, '$.content'), chat_attachment_leases.name) > 0 THEN 1
               WHEN json_type(NEW.request_json, '$.content') IS NOT 'array' THEN instr(json_extract(NEW.request_json, '$.content'), chat_attachment_leases.name) > 0
               ELSE EXISTS (SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
                   WHERE instr(block.value, chat_attachment_leases.name) > 0
                     AND (block.type IS NOT 'object' OR json_extract(block.value, '$.type') IS NOT 'image')) END)
           OR (CASE
                  WHEN instr(NEW.request_json, chat_attachment_leases.name) = 0 THEN 0
                  WHEN NOT json_valid(NEW.request_json) THEN 0
                  WHEN json_type(NEW.request_json) IS NOT 'object' THEN 0
                  WHEN json_type(NEW.request_json, '$.content') IS NOT 'array' THEN 0
                  ELSE (EXISTS (SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
                      WHERE block.type = 'object'
                        AND json_extract(block.value, '$.type') = 'image'
                        AND json_extract(block.value, '$.uri') = '.ao/attachments/' || chat_attachment_leases.name)
                    AND chat_attachment_leases.session_id IN (
                        SELECT session_id FROM conversations WHERE id = NEW.conversation_id
                        UNION SELECT current_session_id FROM conversations WHERE id = NEW.conversation_id)) END));
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_insert;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_update;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_steer;
DROP TRIGGER IF EXISTS chat_attachment_leases_commit_edit;

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_insert
AFTER INSERT ON conversation_messages
WHEN NEW.role = 'user'
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE reclaiming_at_ms IS NOT NULL AND reclaimed_at_ms IS NULL
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
        WHERE reclaiming_at_ms IS NOT NULL AND reclaimed_at_ms IS NULL
          AND (instr(NEW.text, name) > 0 OR instr(NEW.delivery_content_json, name) > 0)
          AND instr(OLD.text, name) = 0 AND instr(OLD.delivery_content_json, name) = 0
    );
    DELETE FROM chat_attachment_leases
    WHERE reclaiming_at_ms IS NULL
      AND (instr(NEW.text, name) > 0 OR instr(NEW.delivery_content_json, name) > 0);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_commit_steer
AFTER INSERT ON conversation_steer_deliveries
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM chat_attachment_leases
        WHERE reclaiming_at_ms IS NOT NULL AND reclaimed_at_ms IS NULL AND instr(NEW.request_json, name) > 0
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
        WHERE reclaiming_at_ms IS NOT NULL AND reclaimed_at_ms IS NULL AND instr(NEW.request_json, name) > 0
    );
    DELETE FROM chat_attachment_leases
    WHERE reclaiming_at_ms IS NULL
      AND instr(NEW.request_json, name) > 0;
END;
-- +goose StatementEnd
