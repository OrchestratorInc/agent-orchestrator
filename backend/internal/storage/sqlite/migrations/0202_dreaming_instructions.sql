-- Summary: keep the standing instructions AO adds to every session (the user's own and each project's) as revisions, and mark dreaming automations.
-- +goose Up
CREATE TABLE instruction_revisions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    -- NULL holds the user's own instructions; a project id holds that project's.
    project_id TEXT REFERENCES projects (id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    -- The AO session that saved the revision; NULL when the user saved it.
    session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL,
    -- 1 when a dreaming session saved it. Dreaming must keep the lines of the
    -- latest revision it did not write.
    dreamed    BOOLEAN NOT NULL DEFAULT FALSE CHECK (dreamed IN (0, 1)),
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_instruction_revisions_project ON instruction_revisions (project_id, id);

ALTER TABLE automations ADD COLUMN purpose TEXT NOT NULL DEFAULT '' CHECK (purpose IN ('', 'dreaming'));
CREATE UNIQUE INDEX idx_automations_dreaming_project ON automations (project_id) WHERE purpose = 'dreaming';

-- +goose Down
DROP INDEX idx_automations_dreaming_project;
ALTER TABLE automations DROP COLUMN purpose;
DROP INDEX idx_instruction_revisions_project;
DROP TABLE instruction_revisions;
