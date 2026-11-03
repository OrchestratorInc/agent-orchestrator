-- +goose Up

-- A workflow is a durable plan spanning one or more registered repositories.
-- It deliberately has no project_id: a single workflow may coordinate several
-- projects, while each repository binding retains its own project ownership.
CREATE TABLE workflows (
    id               TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    title            TEXT NOT NULL CHECK (length(trim(title)) > 0),
    goal             TEXT NOT NULL CHECK (length(trim(goal)) > 0),
    owner_session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL,
    state            TEXT NOT NULL DEFAULT 'draft'
        CHECK (state IN ('draft', 'active', 'paused', 'completed', 'failed', 'cancelled')),
    revision         INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL
);

CREATE INDEX idx_workflows_state_updated
    ON workflows (state, updated_at DESC, id);

CREATE TABLE workflow_repositories (
    workflow_id TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    repo_key    TEXT NOT NULL CHECK (length(trim(repo_key)) > 0),
    project_id  TEXT NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    -- Empty path_ref denotes the registered project's root repository.
    path_ref    TEXT NOT NULL DEFAULT '',
    base_sha    TEXT NOT NULL DEFAULT '',
    target_ref  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (workflow_id, repo_key),
    UNIQUE (workflow_id, project_id, path_ref)
);

CREATE INDEX idx_workflow_repositories_project
    ON workflow_repositories (project_id, workflow_id);

CREATE TABLE workflow_tasks (
    id            TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    workflow_id   TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL
        CHECK (kind IN ('analysis', 'development', 'testing', 'review', 'integration', 'approval')),
    title         TEXT NOT NULL CHECK (length(trim(title)) > 0),
    brief         TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN (
            'pending', 'ready', 'claimed', 'running', 'verifying',
            'ready_for_integration', 'awaiting_approval', 'integrating',
            'retry_wait', 'blocked', 'failed', 'cancelled', 'succeeded'
        )),
    priority      INTEGER NOT NULL DEFAULT 0,
    agent_harness TEXT NOT NULL DEFAULT '',
    max_attempts  INTEGER NOT NULL DEFAULT 1 CHECK (max_attempts > 0),
    revision      INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    UNIQUE (workflow_id, id)
);

CREATE INDEX idx_workflow_tasks_state_priority
    ON workflow_tasks (workflow_id, state, priority DESC, created_at, id);

CREATE TABLE workflow_edges (
    workflow_id  TEXT NOT NULL,
    from_task_id TEXT NOT NULL,
    to_task_id   TEXT NOT NULL,
    PRIMARY KEY (workflow_id, from_task_id, to_task_id),
    CHECK (from_task_id <> to_task_id),
    FOREIGN KEY (workflow_id, from_task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, to_task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_workflow_edges_to_task
    ON workflow_edges (workflow_id, to_task_id, from_task_id);

CREATE TABLE workflow_task_repos (
    workflow_id       TEXT NOT NULL,
    task_id           TEXT NOT NULL,
    repo_key          TEXT NOT NULL,
    allowed_paths_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(allowed_paths_json) AND json_type(allowed_paths_json) = 'array'),
    base_sha          TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (workflow_id, task_id, repo_key),
    FOREIGN KEY (workflow_id, task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, repo_key)
        REFERENCES workflow_repositories (workflow_id, repo_key) ON DELETE CASCADE
);

CREATE INDEX idx_workflow_task_repos_repo
    ON workflow_task_repos (workflow_id, repo_key, task_id);

CREATE TABLE workflow_attempts (
    id            TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    workflow_id   TEXT NOT NULL,
    task_id       TEXT NOT NULL,
    attempt_no    INTEGER NOT NULL CHECK (attempt_no > 0),
    session_id    TEXT REFERENCES sessions (id) ON DELETE SET NULL,
    worker_id     TEXT NOT NULL DEFAULT '',
    lease_token   TEXT NOT NULL DEFAULT '',
    lease_until   TIMESTAMP,
    heartbeat_at  TIMESTAMP,
    state         TEXT NOT NULL
        CHECK (state IN ('claimed', 'running', 'settling', 'succeeded', 'failed', 'unknown')),
    started_at    TIMESTAMP,
    ended_at      TIMESTAMP,
    error_code    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    CHECK (
        state NOT IN ('claimed', 'running', 'settling')
        OR (length(trim(worker_id)) > 0 AND length(trim(lease_token)) > 0 AND lease_until IS NOT NULL)
    ),
    UNIQUE (workflow_id, task_id, attempt_no),
    FOREIGN KEY (workflow_id, task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE
);

-- An UNKNOWN attempt is intentionally included: until a human or a later
-- reconciliation proves its outcome, the task must not be claimed again.
CREATE UNIQUE INDEX idx_workflow_attempts_one_active_per_task
    ON workflow_attempts (workflow_id, task_id)
    WHERE state IN ('claimed', 'running', 'settling', 'unknown');

CREATE INDEX idx_workflow_attempts_lease
    ON workflow_attempts (state, lease_until)
    WHERE lease_until IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_workflow_attempts_lease;
DROP INDEX IF EXISTS idx_workflow_attempts_one_active_per_task;
DROP TABLE IF EXISTS workflow_attempts;
DROP INDEX IF EXISTS idx_workflow_task_repos_repo;
DROP TABLE IF EXISTS workflow_task_repos;
DROP INDEX IF EXISTS idx_workflow_edges_to_task;
DROP TABLE IF EXISTS workflow_edges;
DROP INDEX IF EXISTS idx_workflow_tasks_state_priority;
DROP TABLE IF EXISTS workflow_tasks;
DROP INDEX IF EXISTS idx_workflow_repositories_project;
DROP TABLE IF EXISTS workflow_repositories;
DROP INDEX IF EXISTS idx_workflows_state_updated;
DROP TABLE IF EXISTS workflows;
