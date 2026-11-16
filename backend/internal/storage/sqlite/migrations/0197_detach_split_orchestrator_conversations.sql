-- +goose Up
-- Older builds left a Chat orchestrator bound to two conversations (#6523): the
-- session-scoped row that held what the user typed while it provisioned, and the
-- project row its controller ran on. Every reader keyed on current_session_id
-- could pick the stale row, including the hibernation CDC trigger, the branch-root
-- provider trigger, and the agent-override reset. Detach the session-scoped row so
-- the project row is the only one the session holds. Its prompts were never
-- delivered and are stale once the orchestrator has run, so they are not replayed.
--
-- A session still waiting for its first controller keeps both rows: that
-- controller's start moves the queued prompts into the project row and detaches
-- the session row itself.
UPDATE conversations
SET current_session_id = NULL
WHERE scope = 'session'
  AND current_session_id IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM conversations AS project
      WHERE project.scope = 'project'
        AND project.current_session_id = conversations.current_session_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM sessions
      WHERE sessions.id = conversations.current_session_id
        AND sessions.provision_state = 'provisioning'
        AND sessions.controller_generation = ''
        AND sessions.provider_conversation_id = ''
  );

-- +goose Down
-- Data-only: the detached rows cannot be told apart from rows a later build
-- detached, so there is nothing safe to rebind.
SELECT 1;
