-- name: UpdateSessionFromActivitySignal :execrows
-- Lifecycle reads the session before reducing a hook. Fence the resulting
-- narrow write against a concurrent controller transition.
UPDATE sessions SET
    activity_state = sqlc.arg(activity_state),
    activity_last_at = sqlc.arg(activity_last_at),
    first_signal_at = sqlc.arg(first_signal_at),
    agent_session_id = sqlc.arg(agent_session_id),
    agent_session_id_launch_id = sqlc.arg(agent_session_id_launch_id),
    native_identity_observed_at = sqlc.arg(native_identity_observed_at),
    latest_user_prompt = sqlc.arg(latest_user_prompt),
    latest_user_prompt_at = sqlc.arg(latest_user_prompt_at),
    latest_assistant_update = sqlc.arg(latest_assistant_update),
    latest_assistant_update_at = sqlc.arg(latest_assistant_update_at),
    conversation_checkpoint_state = sqlc.arg(conversation_checkpoint_state),
    conversation_checkpoint_generation = sqlc.arg(conversation_checkpoint_generation),
    conversation_checkpoint_native_id = sqlc.arg(conversation_checkpoint_native_id),
    conversation_checkpoint_unsettled = sqlc.arg(conversation_checkpoint_unsettled),
    conversation_checkpoint_turn_id = sqlc.arg(conversation_checkpoint_turn_id),
    native_checkpoint_evidence = sqlc.arg(native_checkpoint_evidence),
    native_transcript_path = sqlc.arg(native_transcript_path),
    claude_activity_facts = sqlc.arg(claude_activity_facts),
    codex_activity_facts = sqlc.arg(codex_activity_facts),
    -- Only a prompt carrying an AO delivery id writes accepted deliveries, so a
    -- human prompt never erases them. The launch's last 8 are kept, space
    -- separated, so an earlier delivery is still found after a later one.
    accepted_delivery_id = CASE
        WHEN CAST(sqlc.arg(accepted_delivery_id) AS TEXT) = '' THEN accepted_delivery_id
        WHEN accepted_delivery_id = '' OR accepted_delivery_launch_id <> runtime_launch_id
            THEN CAST(sqlc.arg(accepted_delivery_id) AS TEXT)
        WHEN instr(' ' || accepted_delivery_id || ' ', ' ' || CAST(sqlc.arg(accepted_delivery_id) AS TEXT) || ' ') > 0
            THEN accepted_delivery_id
        WHEN length(accepted_delivery_id) - length(replace(accepted_delivery_id, ' ', '')) >= 7
            THEN substr(accepted_delivery_id, instr(accepted_delivery_id, ' ') + 1) || ' ' || CAST(sqlc.arg(accepted_delivery_id) AS TEXT)
        ELSE accepted_delivery_id || ' ' || CAST(sqlc.arg(accepted_delivery_id) AS TEXT)
    END,
    accepted_delivery_launch_id = CASE WHEN CAST(sqlc.arg(accepted_delivery_id) AS TEXT) = ''
        THEN accepted_delivery_launch_id ELSE runtime_launch_id END,
    updated_at = sqlc.arg(updated_at)
WHERE sessions.id = sqlc.arg(id)
  AND sessions.revision = sqlc.arg(expected_revision)
  AND sessions.is_terminated = 0
  AND sessions.harness = sqlc.arg(expected_harness)
  AND sessions.session_mode = sqlc.arg(expected_session_mode)
  AND ((sqlc.arg(expected_session_mode) <> 'chat' AND sessions.runtime_launch_id = sqlc.arg(expected_runtime_launch_id))
    OR (sqlc.arg(expected_session_mode) = 'chat' AND sessions.controller_generation = sqlc.arg(expected_controller_generation)));

-- name: SessionAcceptedDelivery :one
SELECT EXISTS (
    SELECT 1 FROM sessions
    WHERE id = sqlc.arg(id)
      AND instr(' ' || accepted_delivery_id || ' ', ' ' || CAST(sqlc.arg(delivery_id) AS TEXT) || ' ') > 0
      AND accepted_delivery_launch_id = sqlc.arg(runtime_launch_id)
);
