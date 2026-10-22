-- +goose Up

-- What the composer lease holder is typing, shown live to everyone else in
-- the shared session the way a document editor shows a collaborator's text
-- and cursor. Only the holder writes it; it clears whenever the lease is
-- released or passes to someone else, so a draft never outlives its writer's
-- turn. draft_cursor is the caret's offset into draft_text as the writing
-- client counts it. draft_revision moves on every change, so live streams can
-- be woken for a draft change alone.
ALTER TABLE ao_composer_leases
    ADD COLUMN draft_text TEXT NOT NULL DEFAULT '' CHECK (octet_length(draft_text) <= 65536),
    ADD COLUMN draft_cursor INTEGER NOT NULL DEFAULT 0 CHECK (draft_cursor >= 0),
    ADD COLUMN draft_revision BIGINT NOT NULL DEFAULT 0;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ao_notify_composer_lease() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (TG_OP = 'INSERT' AND NEW.holder_user_id IS NOT NULL)
        OR (TG_OP = 'UPDATE' AND (
            OLD.holder_user_id IS DISTINCT FROM NEW.holder_user_id
            OR OLD.epoch <> NEW.epoch
            OR OLD.draft_revision <> NEW.draft_revision
        )) THEN
        PERFORM pg_notify('ao_session_live', NEW.session_id::text);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ao_notify_composer_lease() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (TG_OP = 'INSERT' AND NEW.holder_user_id IS NOT NULL)
        OR (TG_OP = 'UPDATE' AND OLD.holder_user_id IS DISTINCT FROM NEW.holder_user_id)
        OR (TG_OP = 'UPDATE' AND OLD.epoch <> NEW.epoch) THEN
        PERFORM pg_notify('ao_session_live', NEW.session_id::text);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

ALTER TABLE ao_composer_leases
    DROP COLUMN IF EXISTS draft_revision,
    DROP COLUMN IF EXISTS draft_cursor,
    DROP COLUMN IF EXISTS draft_text;
