-- +goose Up
ALTER TABLE pr ADD COLUMN ownership_source TEXT NOT NULL DEFAULT ''
    CHECK (ownership_source IN ('', 'discovered', 'claimed'));

WITH claims AS (
    SELECT CASE WHEN json_valid(detail_json) THEN detail_json ELSE '{}' END AS detail
    FROM conversation_activities
    WHERE kind = 'ao_action' AND status = 'completed'
      AND NOT EXISTS (
          SELECT 1 FROM conversation_messages
          LEFT JOIN conversation_turns ON conversation_turns.id = conversation_messages.turn_id
          WHERE (conversation_messages.turn_id = conversation_activities.turn_id
              OR conversation_turns.promoted_to_turn_id = conversation_activities.turn_id)
            AND conversation_messages.role = 'user'
            AND conversation_messages.origin = 'automation'
            AND conversation_messages.authored_by_user = 0
            AND conversation_messages.sender_session_id = ''
      )
)
UPDATE pr SET ownership_source = 'claimed'
WHERE EXISTS (
    SELECT 1 FROM claims
    WHERE json_extract(detail, '$.action') = 'pull_request.claimed'
      AND json_extract(detail, '$.targetSessionId') = pr.session_id
      AND (
          json_extract(detail, '$.href') = pr.url
          OR json_extract(detail, '$.href') IN (
              SELECT alias_url FROM pr_url_alias WHERE canonical_url = pr.url
          )
      )
);

-- +goose Down
ALTER TABLE pr DROP COLUMN ownership_source;
