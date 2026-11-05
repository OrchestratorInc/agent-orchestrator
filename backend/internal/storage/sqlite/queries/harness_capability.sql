-- name: UpsertHarnessCapabilitySnapshot :exec
-- Harness-scoped capability evidence. A probe replaces the previous observation
-- for the same harness: keeping an older one would let stale support outlive the
-- provider that reported it.
INSERT INTO harness_capability_snapshots (
    harness, source, auth_state, readiness, surfaces,
    streaming, tools, interrupt, steer, queue,
    resume, approval, apply_at, usage, models_json,
    executable, version, reason_code, observed_at, expires_at
) VALUES (
    sqlc.arg(harness), sqlc.arg(source), sqlc.arg(auth_state), sqlc.arg(readiness),
    sqlc.arg(surfaces), sqlc.arg(streaming), sqlc.arg(tools), sqlc.arg(interrupt),
    sqlc.arg(steer), sqlc.arg(queue), sqlc.arg(resume), sqlc.arg(approval),
    sqlc.arg(apply_at), sqlc.arg(usage), sqlc.narg(models_json),
    sqlc.arg(executable), sqlc.arg(version), sqlc.arg(reason_code),
    sqlc.arg(observed_at), sqlc.arg(expires_at)
)
ON CONFLICT (harness) DO UPDATE SET
    source      = excluded.source,
    auth_state  = excluded.auth_state,
    readiness   = excluded.readiness,
    surfaces    = excluded.surfaces,
    streaming   = excluded.streaming,
    tools       = excluded.tools,
    interrupt   = excluded.interrupt,
    steer       = excluded.steer,
    queue       = excluded.queue,
    resume      = excluded.resume,
    approval    = excluded.approval,
    apply_at    = excluded.apply_at,
    usage       = excluded.usage,
    models_json = excluded.models_json,
    executable  = excluded.executable,
    version     = excluded.version,
    reason_code = excluded.reason_code,
    observed_at = excluded.observed_at,
    expires_at  = excluded.expires_at;

-- name: GetHarnessCapabilitySnapshot :one
SELECT * FROM harness_capability_snapshots WHERE harness = sqlc.arg(harness);

-- name: UpsertSessionCapabilitySnapshot :exec
-- Session-scoped capability evidence. One row per session: the newest
-- observation replaces the previous generation's, which is what makes an
-- expired generation unable to authorize anything.
INSERT INTO session_capability_snapshots (
    session_id, harness, generation, source, auth_state, readiness, surfaces,
    streaming, tools, interrupt, steer, queue,
    resume, approval, apply_at, usage, models_json,
    executable, version, reason_code, observed_at, expires_at
) VALUES (
    sqlc.arg(session_id), sqlc.arg(harness), sqlc.arg(generation), sqlc.arg(source),
    sqlc.arg(auth_state), sqlc.arg(readiness), sqlc.arg(surfaces),
    sqlc.arg(streaming), sqlc.arg(tools), sqlc.arg(interrupt), sqlc.arg(steer),
    sqlc.arg(queue), sqlc.arg(resume), sqlc.arg(approval), sqlc.arg(apply_at),
    sqlc.arg(usage), sqlc.narg(models_json), sqlc.arg(executable),
    sqlc.arg(version), sqlc.arg(reason_code), sqlc.arg(observed_at),
    sqlc.arg(expires_at)
)
ON CONFLICT (session_id) DO UPDATE SET
    harness     = excluded.harness,
    generation  = excluded.generation,
    source      = excluded.source,
    auth_state  = excluded.auth_state,
    readiness   = excluded.readiness,
    surfaces    = excluded.surfaces,
    streaming   = excluded.streaming,
    tools       = excluded.tools,
    interrupt   = excluded.interrupt,
    steer       = excluded.steer,
    queue       = excluded.queue,
    resume      = excluded.resume,
    approval    = excluded.approval,
    apply_at    = excluded.apply_at,
    usage       = excluded.usage,
    models_json = excluded.models_json,
    executable  = excluded.executable,
    version     = excluded.version,
    reason_code = excluded.reason_code,
    observed_at = excluded.observed_at,
    expires_at  = excluded.expires_at;

-- name: GetSessionCapabilitySnapshot :one
SELECT * FROM session_capability_snapshots WHERE session_id = sqlc.arg(session_id);
