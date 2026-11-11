-- name: ListParentOrchestrators :many
WITH RECURSIVE ancestry(child_id, ancestor_id) AS (
    SELECT id, id FROM sessions
    WHERE id IN (SELECT value FROM json_each(sqlc.arg(session_ids)))
      AND kind <> 'orchestrator'
    UNION
    SELECT ancestry.child_id, parent.id
    FROM ancestry
    JOIN sessions AS child ON child.id = ancestry.ancestor_id
    LEFT JOIN conversation_activities AS activity
      ON child.spawn_parent_session_id = ''
     AND child.orchestrator_session_id = ''
     AND activity.provider_item_id = 'ao-action:session.spawned:' || child.id
     AND activity.provider_item_id <> ''
     AND activity.kind = 'ao_action'
     AND activity.status = 'completed'
    JOIN sessions AS parent ON parent.id = CASE
        WHEN child.spawn_parent_session_id <> '' THEN child.spawn_parent_session_id
        WHEN child.orchestrator_session_id <> '' THEN child.orchestrator_session_id
        WHEN json_valid(activity.detail_json) THEN json_extract(activity.detail_json, '$.sourceSessionId')
    END
    WHERE child.kind <> 'orchestrator'
      AND parent.id <> child.id
      AND parent.project_id IS child.project_id
      AND (child.spawn_parent_session_id <> '' OR child.orchestrator_session_id = ''
           OR (parent.kind = 'orchestrator' AND parent.independent_orchestrator = 1))
)
SELECT CAST(ancestry.child_id AS TEXT) AS child_id, CAST(MIN(parent.id) AS TEXT) AS parent_id
FROM ancestry
JOIN sessions AS parent ON parent.id = ancestry.ancestor_id
WHERE parent.kind = 'orchestrator'
GROUP BY ancestry.child_id
HAVING COUNT(DISTINCT parent.id) = 1;
