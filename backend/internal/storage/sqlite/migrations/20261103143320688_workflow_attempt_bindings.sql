-- +goose Up

-- M2 attempt→session binding.
--
-- M1 recorded an attempt and left workflow_attempts.session_id nullable. M2 adds a
-- separate binding row that records the INTENT to spawn, the request identity that
-- makes a retry safe, and the session that finally answered. The two session_id
-- columns are written in one transaction so they can never disagree.
--
-- Nothing here stores a lease token. The lease remains the only capability that
-- authorizes work, and it stays in workflow_attempts where exactly one response may
-- return it.

-- A composite foreign key needs a unique parent key on the exact column list. The
-- attempt's primary key already makes this index redundant for uniqueness; it exists
-- so (id, workflow_id, task_id) can be referenced and a binding cannot claim an
-- attempt from a different task.
CREATE UNIQUE INDEX idx_workflow_attempts_identity
    ON workflow_attempts (id, workflow_id, task_id);

CREATE TABLE workflow_attempt_bindings (
    attempt_id            TEXT PRIMARY KEY NOT NULL CHECK (length(trim(attempt_id)) > 0),
    workflow_id           TEXT NOT NULL CHECK (length(trim(workflow_id)) > 0),
    task_id               TEXT NOT NULL CHECK (length(trim(task_id)) > 0),

    -- spawn_request_id is derived from the attempt identity, so a retried Bind
    -- replays instead of spawning a second session. UNIQUE is what makes that a
    -- database guarantee rather than a convention.
    spawn_request_id      TEXT NOT NULL CHECK (length(trim(spawn_request_id)) > 0),
    -- spawn_hash is the 64-character request fingerprint, matching the Workflow
    -- idempotency ledger's rule: a different hash under the same request id is a
    -- conflict, not a retry.
    spawn_hash            TEXT NOT NULL CHECK (length(spawn_hash) = 64),

    state                 TEXT NOT NULL DEFAULT 'intent_persisted'
        CHECK (state IN (
            'unbound', 'intent_persisted', 'spawn_requested', 'session_provisioning',
            'bound_ready', 'running', 'spawn_unknown', 'reconcile_required',
            'failed_confirmed', 'stop_requested', 'stop_confirmed', 'stop_unknown',
            'recovery_uncertain'
        )),

    -- NULL until a session actually exists. UNIQUE is the load-bearing constraint:
    -- one AO session may belong to at most one attempt binding, so two attempts can
    -- never share a session and later fight over its controller.
    session_id            TEXT UNIQUE REFERENCES sessions (id) ON DELETE SET NULL,
    controller_generation TEXT NOT NULL DEFAULT '',
    bound_at              TIMESTAMP,
    failure_code          TEXT NOT NULL DEFAULT '',
    revision              INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at            TIMESTAMP NOT NULL,
    updated_at            TIMESTAMP NOT NULL,

    -- A binding in a session-bearing state must name its session; a state that
    -- precedes spawning must not claim one.
    --
    -- This CHECK stays strict on purpose. Loosening it to tolerate a NULL session would
    -- let a bound_ready row survive its session's deletion while still claiming to be
    -- bound, which is the divergence the whole design forbids. Session deletion is
    -- handled by the BEFORE DELETE trigger below instead, which records the loss as a
    -- fact before the foreign key can null the column.
    CHECK (
        state NOT IN ('session_provisioning', 'bound_ready', 'running', 'stop_requested',
                      'stop_confirmed', 'stop_unknown', 'recovery_uncertain')
        OR (session_id IS NOT NULL AND bound_at IS NOT NULL)
    ),
    CHECK (state NOT IN ('unbound', 'intent_persisted', 'spawn_requested')
        OR session_id IS NULL),

    UNIQUE (spawn_request_id),
    -- Redundant with the primary key, declared so repo bindings can reference the
    -- (attempt, workflow) pair and a repo row cannot cross into another workflow.
    UNIQUE (attempt_id, workflow_id),
    FOREIGN KEY (attempt_id, workflow_id, task_id)
        REFERENCES workflow_attempts (id, workflow_id, task_id) ON DELETE CASCADE
);

CREATE INDEX idx_workflow_attempt_bindings_session
    ON workflow_attempt_bindings (session_id)
    WHERE session_id IS NOT NULL;

CREATE INDEX idx_workflow_attempt_bindings_state
    ON workflow_attempt_bindings (workflow_id, state, updated_at);

-- Per-repository binding facts. Each repository is verified independently: a
-- multi-repository task can have one worktree at the expected base SHA and another
-- drifted, and collapsing that into one flag would hide the drift.
CREATE TABLE workflow_attempt_repo_bindings (
    attempt_id        TEXT NOT NULL,
    workflow_id       TEXT NOT NULL,
    repo_key          TEXT NOT NULL CHECK (length(trim(repo_key)) > 0),
    project_id        TEXT NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    expected_base_sha TEXT NOT NULL DEFAULT '',
    -- session_worktree_ref is a reference into the existing worktree records; M2
    -- never duplicates Git state here.
    session_worktree_ref TEXT NOT NULL DEFAULT '',
    observed_head_sha TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'verified', 'mismatch', 'missing', 'unknown')),
    revision          INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at        TIMESTAMP NOT NULL,

    PRIMARY KEY (attempt_id, repo_key),
    FOREIGN KEY (attempt_id, workflow_id)
        REFERENCES workflow_attempt_bindings (attempt_id, workflow_id) ON DELETE CASCADE,
    -- Cross-workflow rejection is a real foreign key, not a store convention: a
    -- repo_key that this workflow never registered cannot be inserted at all.
    FOREIGN KEY (workflow_id, repo_key)
        REFERENCES workflow_repositories (workflow_id, repo_key) ON DELETE CASCADE
);

CREATE INDEX idx_workflow_attempt_repo_bindings_workflow
    ON workflow_attempt_repo_bindings (workflow_id, repo_key);

-- Session deletion is a legitimate user action (DeleteTerminatedSession), and a binding
-- must not block it. The FK's ON DELETE SET NULL alone cannot survive the coherence CHECK,
-- because SQLite applies the action and the CHECK in one statement, so the delete fails
-- with a raw "CHECK constraint failed" the user cannot act on.
--
-- This BEFORE DELETE trigger records the loss as a durable fact first: the binding moves to
-- reconcile_required with a stable failure_code, then the FK nulls session_id legally
-- because reconcile_required is one of the states allowed to hold no session.
--
-- reconcile_required is the honest target rather than a "deleted" state: the attempt's
-- outcome is unknown, and the recovery path already treats that state as "gather evidence,
-- never re-spawn". A reader must not see bound_ready or running afterwards.
-- +goose StatementBegin
CREATE TRIGGER sessions_workflow_binding_loss
BEFORE DELETE ON sessions
BEGIN
    UPDATE workflow_attempt_bindings
    SET state = 'reconcile_required',
        session_id = NULL,
        bound_at = NULL,
        controller_generation = '',
        failure_code = 'session_deleted',
        revision = revision + 1,
        updated_at = OLD.updated_at
    WHERE session_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS sessions_workflow_binding_loss;
DROP INDEX IF EXISTS idx_workflow_attempt_repo_bindings_workflow;
DROP TABLE IF EXISTS workflow_attempt_repo_bindings;
DROP INDEX IF EXISTS idx_workflow_attempt_bindings_state;
DROP INDEX IF EXISTS idx_workflow_attempt_bindings_session;
DROP TABLE IF EXISTS workflow_attempt_bindings;
DROP INDEX IF EXISTS idx_workflow_attempts_identity;
