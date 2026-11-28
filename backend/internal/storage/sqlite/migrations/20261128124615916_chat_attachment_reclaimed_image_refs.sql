-- +goose Up
-- A chat message can send an image by its staged path; the daemon reads the
-- file, sends its bytes and records the path as the image block's uri, which
-- commits the draft's lease (chat_attachment_leases). The read comes before
-- the write that accepts the message. If the draft released the file and
-- reclamation deleted it in between, the commit triggers let the name through
-- (a reclaimed file's name is just text), so history would name a file that
-- is gone and a retry of the same message would fail on it before reaching
-- its receipt. An image block naming a file that reclamation claimed is
-- refused instead, in the same write. Text and other blocks are unchanged.
-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_refuse_reclaimed_image_insert
AFTER INSERT ON conversation_messages
WHEN NEW.role = 'user' AND NEW.delivery_content_json <> '' AND json_valid(NEW.delivery_content_json)
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
        JOIN chat_attachment_leases AS lease
          ON json_extract(block.value, '$.uri') = '.ao/attachments/' || lease.name
        WHERE json_extract(block.value, '$.type') = 'image'
          AND lease.reclaiming_at_ms IS NOT NULL
    );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_refuse_reclaimed_image_update
AFTER UPDATE OF delivery_content_json ON conversation_messages
WHEN NEW.role = 'user' AND NEW.delivery_content_json <> '' AND json_valid(NEW.delivery_content_json)
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM json_each(NEW.delivery_content_json) AS block
        JOIN chat_attachment_leases AS lease
          ON json_extract(block.value, '$.uri') = '.ao/attachments/' || lease.name
        WHERE json_extract(block.value, '$.type') = 'image'
          AND lease.reclaiming_at_ms IS NOT NULL
          AND instr(OLD.delivery_content_json, lease.name) = 0
    );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER chat_attachment_leases_refuse_reclaimed_image_steer
AFTER INSERT ON conversation_steer_deliveries
WHEN json_valid(NEW.request_json)
BEGIN
    SELECT RAISE(ABORT, 'chat attachment was discarded')
    WHERE EXISTS (
        SELECT 1 FROM json_each(NEW.request_json, '$.content') AS block
        JOIN chat_attachment_leases AS lease
          ON json_extract(block.value, '$.uri') = '.ao/attachments/' || lease.name
        WHERE json_extract(block.value, '$.type') = 'image'
          AND lease.reclaiming_at_ms IS NOT NULL
    );
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS chat_attachment_leases_refuse_reclaimed_image_steer;
DROP TRIGGER IF EXISTS chat_attachment_leases_refuse_reclaimed_image_update;
DROP TRIGGER IF EXISTS chat_attachment_leases_refuse_reclaimed_image_insert;
-- +goose StatementEnd
