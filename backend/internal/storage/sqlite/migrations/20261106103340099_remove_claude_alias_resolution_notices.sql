-- +goose Up

-- Claude chats used to record a "model.rerouted" notice whenever an alias resolved
-- to a concrete model, so "opus" answered by claude-opus-5-5 read as a swap. The
-- daemon now records only real substitutions, but the old notices stay on the
-- timeline, and the record stays on the conversation, until they are removed.
--
-- They are found by the exact reason that code wrote, which later notices do not
-- use, so a Codex reroute or a new Claude substitution is never matched. An answer
-- from another family than the alias named really was a swap and is kept. The
-- provider catalog of the day is gone, so an older version within the family can no
-- longer be told from normal resolution and is removed with the rest; if it is still
-- happening the daemon records it again on the next turn. The raw provider-event
-- archive is left alone: nothing replays it into the timeline.
DELETE FROM conversation_activities
WHERE kind = 'system'
  AND json_valid(detail_json)
  AND json_extract(detail_json, '$.event') = 'model.rerouted'
  AND json_extract(detail_json, '$.reason') = printf(
        'The installed Claude Code resolves "%s" to %s',
        json_extract(detail_json, '$.fromModel'),
        json_extract(detail_json, '$.toModel'))
  AND (
    lower(json_extract(detail_json, '$.fromModel')) = 'default'
    OR instr(
        lower(json_extract(detail_json, '$.toModel')),
        lower(CASE
          WHEN instr(json_extract(detail_json, '$.fromModel'), '[') > 0
          THEN substr(json_extract(detail_json, '$.fromModel'), 1,
                      instr(json_extract(detail_json, '$.fromModel'), '[') - 1)
          ELSE json_extract(detail_json, '$.fromModel')
        END)) > 0
  );

UPDATE conversations
SET model_reroute_json = NULL
WHERE model_reroute_json IS NOT NULL
  AND json_valid(model_reroute_json)
  AND json_extract(model_reroute_json, '$.reason') = printf(
        'The installed Claude Code resolves "%s" to %s',
        json_extract(model_reroute_json, '$.fromModel'),
        json_extract(model_reroute_json, '$.toModel'))
  AND (
    lower(json_extract(model_reroute_json, '$.fromModel')) = 'default'
    OR instr(
        lower(json_extract(model_reroute_json, '$.toModel')),
        lower(CASE
          WHEN instr(json_extract(model_reroute_json, '$.fromModel'), '[') > 0
          THEN substr(json_extract(model_reroute_json, '$.fromModel'), 1,
                      instr(json_extract(model_reroute_json, '$.fromModel'), '[') - 1)
          ELSE json_extract(model_reroute_json, '$.fromModel')
        END)) > 0
  );

-- +goose Down
-- A data repair: the removed notices described no substitution, so there is nothing
-- to restore.
