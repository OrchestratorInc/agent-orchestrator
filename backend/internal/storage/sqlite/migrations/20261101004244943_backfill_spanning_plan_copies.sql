-- +goose Up

-- Claude's task list is one list the agent keeps across turns, and the daemon now
-- marks every copy but the latest superseded. Copies written before that carry no
-- spansTurns flag, so the daemon could never mark them: a turnless plan row (one
-- per revision, before plan rows were keyed per run) and a turn plan from before
-- the Claude driver set the flag. Flag them from durable evidence, then mark every
-- copy but each conversation's latest superseded, as the daemon would have.

-- A plan row is Claude's when its turn's plan already carries the flag the Claude
-- driver sets, or when Claude Code was the conversation's harness at the row's
-- position: the target of the last provider switch before it, else the source of
-- the first switch after it, else the session's harness. Review conversations are
-- left to the first rule, since their session's harness is not the reviewer's.
CREATE TEMP TABLE legacy_spanning_plan_rows AS
SELECT a.id, a.turn_id
FROM conversation_activities a
JOIN conversations c ON c.id = a.conversation_id
WHERE a.kind = 'plan'
  AND json_valid(a.detail_json)
  AND json_extract(a.detail_json, '$.spansTurns') IS NULL
  AND (
    EXISTS (
      SELECT 1 FROM conversation_turns t
      WHERE t.id = a.turn_id
        AND t.plan_json <> ''
        AND json_valid(t.plan_json)
        AND json_extract(t.plan_json, '$.spansTurns') = 1
    )
    OR (
      COALESCE(c.review_id, '') = ''
      AND COALESCE(
        (SELECT json_extract(m.detail_json, '$.targetHarness')
         FROM conversation_activities m
         WHERE m.conversation_id = a.conversation_id
           AND m.kind = 'system'
           AND json_valid(m.detail_json)
           AND json_extract(m.detail_json, '$.event') = 'provider_switch'
           AND m.sequence < a.sequence
         ORDER BY m.sequence DESC LIMIT 1),
        (SELECT json_extract(m.detail_json, '$.sourceHarness')
         FROM conversation_activities m
         WHERE m.conversation_id = a.conversation_id
           AND m.kind = 'system'
           AND json_valid(m.detail_json)
           AND json_extract(m.detail_json, '$.event') = 'provider_switch'
           AND m.sequence > a.sequence
         ORDER BY m.sequence ASC LIMIT 1),
        (SELECT s.harness FROM sessions s
         WHERE s.id = COALESCE(NULLIF(c.session_id, ''), NULLIF(c.current_session_id, '')))
      ) = 'claude-code'
    )
  );

UPDATE conversation_activities
SET detail_json = json_set(detail_json, '$.spansTurns', json('true'))
WHERE id IN (SELECT id FROM legacy_spanning_plan_rows);

-- A turn plan and its turn's plan row are one copy, written from one event.
UPDATE conversation_turns
SET plan_json = json_set(plan_json, '$.spansTurns', json('true'))
WHERE plan_json <> ''
  AND json_valid(plan_json)
  AND json_extract(plan_json, '$.spansTurns') IS NULL
  AND id IN (SELECT turn_id FROM legacy_spanning_plan_rows WHERE turn_id IS NOT NULL);

-- Each conversation's latest copy is the one whose content was written last. Every
-- copy has a plan row, a turn's included, so the rows decide it. updated_at is no
-- guide: settling a turn or recovering a controller touches rows it does not
-- rewrite. A turnless row was one revision, written when it was created; a turn's
-- copy can change only until its turn ends.
CREATE TEMP TABLE current_spanning_plan_copies AS
SELECT conversation_id, id, turn_id
FROM (
  SELECT a.conversation_id, a.id, a.turn_id,
         ROW_NUMBER() OVER (
           PARTITION BY a.conversation_id
           ORDER BY CASE
                      WHEN a.turn_id IS NULL THEN a.created_at
                      ELSE COALESCE((SELECT t.completed_at FROM conversation_turns t WHERE t.id = a.turn_id), a.updated_at)
                    END DESC,
                    a.sequence DESC
         ) AS rank
  FROM conversation_activities a
  WHERE a.kind = 'plan'
    AND json_valid(a.detail_json)
    AND json_extract(a.detail_json, '$.spansTurns') = 1
    AND json_extract(a.detail_json, '$.superseded') IS NULL
)
WHERE rank = 1;

UPDATE conversation_activities
SET detail_json = json_set(detail_json, '$.superseded', json('true'))
WHERE kind = 'plan'
  AND json_valid(detail_json)
  AND json_extract(detail_json, '$.spansTurns') = 1
  AND json_extract(detail_json, '$.superseded') IS NULL
  AND conversation_id IN (SELECT conversation_id FROM current_spanning_plan_copies)
  AND id NOT IN (SELECT id FROM current_spanning_plan_copies);

UPDATE conversation_turns
SET plan_json = json_set(plan_json, '$.superseded', json('true'))
WHERE plan_json <> ''
  AND json_valid(plan_json)
  AND json_extract(plan_json, '$.spansTurns') = 1
  AND json_extract(plan_json, '$.superseded') IS NULL
  AND conversation_id IN (SELECT conversation_id FROM current_spanning_plan_copies)
  AND id NOT IN (SELECT turn_id FROM current_spanning_plan_copies WHERE turn_id IS NOT NULL);

DROP TABLE current_spanning_plan_copies;
DROP TABLE legacy_spanning_plan_rows;

-- +goose Down
-- A data repair: the flags it adds are what the daemon writes now, so there is
-- nothing to undo.
