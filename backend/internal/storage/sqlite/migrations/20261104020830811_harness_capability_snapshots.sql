-- +goose Up
-- Durable harness capability evidence, so a capability gate can answer after a
-- restart from what a provider was actually observed doing instead of probing
-- blindly or falling back to a static profile.
--
-- Two tables rather than one, because the two scopes have different identity and
-- different lifetimes. A harness-scoped snapshot describes one harness and is
-- keyed by it; a session-scoped snapshot describes one session's controller
-- generation and is keyed by the session. In one table the natural key would be
-- (scope, harness, COALESCE(session_id, '')) — and SQLite cannot put an
-- expression in a PRIMARY KEY, so the choice is either a redundant scope_key
-- column that a writer can get wrong, or two tables whose keys are exact by
-- construction. Two tables was the simpler correct shape: the scope is the table,
-- and no row can claim a scope it does not store.
--
-- session_id is nullable in the row model but is the primary key here: a
-- session-scoped snapshot always names its session (domain validation refuses one
-- without a generation), so there is no "session-scoped, no session" row to
-- represent.
--
-- The feature columns are three-valued text ('supported'/'unsupported'/'unknown')
-- rather than booleans, because absence of evidence must never be stored as a
-- negative. models_json holds the advertised model catalog as a JSON array of
-- {model, effort, defaultEffort}; it is display metadata, and a row whose catalog
-- cannot be decoded is still usable evidence for everything else.
CREATE TABLE harness_capability_snapshots (
    harness     TEXT PRIMARY KEY NOT NULL,
    source      TEXT NOT NULL,
    auth_state  TEXT NOT NULL,
    readiness   TEXT NOT NULL,
    surfaces    TEXT NOT NULL,
    streaming   TEXT NOT NULL,
    tools       TEXT NOT NULL,
    interrupt   TEXT NOT NULL,
    steer       TEXT NOT NULL,
    queue       TEXT NOT NULL,
    resume      TEXT NOT NULL,
    approval    TEXT NOT NULL,
    apply_at    TEXT NOT NULL,
    usage       TEXT NOT NULL,
    models_json TEXT,
    executable  TEXT NOT NULL DEFAULT '',
    version     TEXT NOT NULL DEFAULT '',
    reason_code TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMP NOT NULL,
    expires_at  TIMESTAMP NOT NULL
);

CREATE TABLE session_capability_snapshots (
    session_id  TEXT PRIMARY KEY NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    harness     TEXT NOT NULL,
    generation  TEXT NOT NULL,
    source      TEXT NOT NULL,
    auth_state  TEXT NOT NULL,
    readiness   TEXT NOT NULL,
    surfaces    TEXT NOT NULL,
    streaming   TEXT NOT NULL,
    tools       TEXT NOT NULL,
    interrupt   TEXT NOT NULL,
    steer       TEXT NOT NULL,
    queue       TEXT NOT NULL,
    resume      TEXT NOT NULL,
    approval    TEXT NOT NULL,
    apply_at    TEXT NOT NULL,
    usage       TEXT NOT NULL,
    models_json TEXT,
    executable  TEXT NOT NULL DEFAULT '',
    version     TEXT NOT NULL DEFAULT '',
    reason_code TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMP NOT NULL,
    expires_at  TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE session_capability_snapshots;
DROP TABLE harness_capability_snapshots;
