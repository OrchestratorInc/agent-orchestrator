-- Summary: keep the small managed-account catalogue, routes and crash-recovery
-- intent separate from reconstructed session metadata. No credentials are stored.
-- Account-manager previews recorded this schema at 171/172, and later
-- development builds at 173, before main assigned those numbers. This version
-- preserves state such a build already created.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS provider_account_state (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
 facts TEXT NOT NULL CHECK (json_valid(facts)),
 pending TEXT CHECK (pending IS NULL OR json_valid(pending))
);
INSERT INTO provider_account_state (id, facts) VALUES (1, '{"revision":0,"accounts":[],"primaries":[],"routes":[]}') ON CONFLICT(id) DO NOTHING;
CREATE TRIGGER IF NOT EXISTS provider_account_routes_cdc
AFTER UPDATE OF facts ON provider_account_state
WHEN NEW.facts <> OLD.facts
BEGIN
 INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
 SELECT s.project_id, s.id, 'session_updated', json_object('id',s.id), datetime('now')
 FROM sessions s WHERE s.id IN (
 SELECT json_extract(value,'$.session_id') FROM json_each(NEW.facts,'$.routes')
 UNION SELECT json_extract(value,'$.session_id') FROM json_each(OLD.facts,'$.routes')
 );
END;
-- +goose StatementEnd
-- +goose Down
-- Preserve routing facts and pending cleanup across preview rollbacks.
