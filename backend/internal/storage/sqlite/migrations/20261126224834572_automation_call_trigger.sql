-- +goose Up
-- A "call" automation runs when an outside service POSTs to /hooks/<hook_key>
-- instead of on a schedule; its rrule_text and timezone stay empty and it is
-- never due. A called run carries the prompt filled from that request, and a
-- dedupe key so the same delivery does not start a second session.
ALTER TABLE automations ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'schedule'
    CHECK (trigger_kind IN ('schedule', 'call'));
ALTER TABLE automations ADD COLUMN hook_key TEXT;
ALTER TABLE automations ADD COLUMN hook_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN hook_signature_header TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN hook_filters TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN hook_dedupe_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_automations_hook_key ON automations (hook_key) WHERE hook_key IS NOT NULL;

ALTER TABLE automation_runs ADD COLUMN prompt TEXT;
ALTER TABLE automation_runs ADD COLUMN dedupe_key TEXT;
CREATE INDEX idx_automation_runs_dedupe ON automation_runs (automation_id, dedupe_key)
    WHERE dedupe_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_automation_runs_dedupe;
ALTER TABLE automation_runs DROP COLUMN dedupe_key;
ALTER TABLE automation_runs DROP COLUMN prompt;
DROP INDEX IF EXISTS idx_automations_hook_key;
ALTER TABLE automations DROP COLUMN hook_dedupe_key;
ALTER TABLE automations DROP COLUMN hook_filters;
ALTER TABLE automations DROP COLUMN hook_signature_header;
ALTER TABLE automations DROP COLUMN hook_secret;
ALTER TABLE automations DROP COLUMN hook_key;
ALTER TABLE automations DROP COLUMN trigger_kind;
