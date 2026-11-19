-- +goose Up

-- A model reroute describes the provider that recorded it. A successful provider
-- switch now clears it, but chats switched before that kept the previous
-- provider's reroute, so a chat switched from Claude Code to Codex still names the
-- Claude model in its composer. Clear a reroute when a provider switch was
-- committed after the last reroute, as the switch would have done. The timeline
-- rows for both stay, so the chat's history is unchanged.
UPDATE conversations
SET model_reroute_json = NULL
WHERE model_reroute_json IS NOT NULL
  AND (
    SELECT MAX(m.sequence)
    FROM conversation_activities m
    WHERE m.conversation_id = conversations.id
      AND m.kind = 'system'
      AND json_valid(m.detail_json)
      AND json_extract(m.detail_json, '$.event') = 'provider_switch'
  ) > (
    SELECT MAX(r.sequence)
    FROM conversation_activities r
    WHERE r.conversation_id = conversations.id
      AND r.kind = 'system'
      AND json_valid(r.detail_json)
      AND json_extract(r.detail_json, '$.event') = 'model.rerouted'
  );

-- +goose Down
-- A data repair: the switch clears the same reroute now, so there is nothing to
-- undo.
