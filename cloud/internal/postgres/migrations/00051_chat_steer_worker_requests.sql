-- Restore the interface commands after 00048 replaced the shared worker
-- request constraint. Preserve the harness commands introduced on main.
-- +goose Up
ALTER TABLE ao_worker_requests DROP CONSTRAINT ao_worker_requests_kind_check;
ALTER TABLE ao_worker_requests ADD CONSTRAINT ao_worker_requests_kind_check CHECK (kind IN (
    'workspace.list', 'workspace.read', 'workspace.write', 'workspace.diff',
    'workspace.diff-file', 'workspace.review.summary', 'workspace.review.tree',
    'workspace.review.search', 'workspace.review.file', 'workspace.review.diffs',
    'workspace.review.revision', 'workspace.review.write',
    'terminal.open', 'terminal.input', 'terminal.resize', 'terminal.close',
    'browser.fetch', 'interface.inspect', 'interface.interrupt', 'interface.stop',
    'interface.native-id', 'interface.start', 'interface.ready', 'chat.models',
    'chat.steer', 'harness.inspect', 'harness.install'
)) NOT VALID;

-- +goose Down
DELETE FROM ao_worker_requests WHERE kind LIKE 'interface.%'
    OR kind IN ('chat.models', 'chat.steer');
ALTER TABLE ao_worker_requests DROP CONSTRAINT ao_worker_requests_kind_check;
ALTER TABLE ao_worker_requests ADD CONSTRAINT ao_worker_requests_kind_check CHECK (kind IN (
    'workspace.list', 'workspace.read', 'workspace.write', 'workspace.diff',
    'workspace.diff-file', 'workspace.review.summary', 'workspace.review.tree',
    'workspace.review.search', 'workspace.review.file', 'workspace.review.diffs',
    'workspace.review.revision', 'workspace.review.write',
    'terminal.open', 'terminal.input', 'terminal.resize', 'terminal.close',
    'browser.fetch', 'harness.inspect', 'harness.install'
)) NOT VALID;
