-- +goose Up
ALTER TABLE projects ADD COLUMN revision INTEGER NOT NULL DEFAULT 0
    CHECK (typeof(revision) = 'integer' AND revision >= 0);

-- Reject revision resets and jumps; an explicit next revision is still monotonic.
-- The AFTER trigger advances writers that omit revision. Its own next-revision
-- write passes the BEFORE guard and does not recurse.
-- +goose StatementBegin
CREATE TRIGGER projects_revision_guard
BEFORE UPDATE ON projects
WHEN NEW.revision <> OLD.revision AND NEW.revision <> OLD.revision + 1
BEGIN
    SELECT RAISE(ABORT, 'project revision must advance by one');
END;
-- +goose StatementEnd

-- Database-owned so every project writer participates, including ABA updates.
-- The guard prevents recursion when recursive_triggers is enabled.
-- Read revisions with SELECT; UPDATE RETURNING precedes this AFTER trigger.
-- +goose StatementBegin
CREATE TRIGGER projects_revision_update
AFTER UPDATE ON projects
WHEN NEW.revision = OLD.revision
BEGIN
    UPDATE projects SET revision = OLD.revision + 1 WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER projects_revision_update;
DROP TRIGGER projects_revision_guard;
ALTER TABLE projects DROP COLUMN revision;
