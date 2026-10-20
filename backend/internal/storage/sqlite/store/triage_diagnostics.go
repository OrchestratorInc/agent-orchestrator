package store

import (
	"context"
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TriageDiagnostic is a bounded private snapshot, not a normal session read.
// Each query can fail independently on an old or damaged database.
type TriageDiagnostic struct {
	Kind    string
	Entries []json.RawMessage
	Err     error
}

// TriageWorkspaceRoot is one registered workspace's name and complete path.
type TriageWorkspaceRoot struct{ Name, Path string }

// ReadTriageSnapshot never enriches, reconciles or writes session state. SQL
// bounds every field before allocation and selects only the exact turn owner.
func (s *Store) ReadTriageSnapshot(ctx context.Context, id domain.SessionID) []TriageDiagnostic {
	queries := []struct{ kind, sql string }{
		{"retained-session", `SELECT json_object(
'harness',substr(harness,1,64),'mode',substr(session_mode,1,64),
'activityState',substr(activity_state,1,64),'provisionState',substr(provision_state,1,64),
'provisionError',CAST(substr(CAST(COALESCE(provision_error,'') AS BLOB),1,4096) AS TEXT),
'workspacePath',CASE WHEN length(CAST(workspace_path AS BLOB))<=4096 THEN workspace_path ELSE '' END,
'createdAt',substr(created_at,1,64),'updatedAt',substr(updated_at,1,64),
'activityLastAt',substr(activity_last_at,1,64),'terminated',json(CASE WHEN is_terminated THEN 'true' ELSE 'false' END))
FROM sessions WHERE id=? LIMIT 1`},
		{"retained-turn", `SELECT json_object('state',substr(state,1,64),
'error',CAST(substr(CAST(COALESCE(error_message,'') AS BLOB),1,4096) AS TEXT),
'requestedAt',substr(requested_at,1,64),'startedAt',substr(started_at,1,64),'completedAt',substr(completed_at,1,64))
FROM conversation_turns WHERE handled_by_session_id=? AND rolled_back_at IS NULL AND promoted_to_turn_id IS NULL
ORDER BY requested_at DESC,rowid DESC LIMIT 1`},
		{"session-history", `WITH owned AS (
 SELECT id,conversation_id,requested_at,rowid AS turn_order,state AS turn_state,rolled_back_at IS NOT NULL AS rolled_back
 FROM conversation_turns WHERE handled_by_session_id=? AND promoted_to_turn_id IS NULL
), timeline AS (
 SELECT m.sequence,'message' AS kind,m.role,'' AS state,t.turn_state,m.text,'' AS detail,'' AS output,'' AS streamed,m.created_at,t.rolled_back,t.requested_at,t.turn_order
 FROM conversation_messages m JOIN owned t ON m.turn_id=t.id AND m.conversation_id=t.conversation_id
 UNION ALL
 SELECT a.sequence,a.kind,'',a.status,t.turn_state,a.summary,a.detail_json,a.command_output,a.streamed_text,a.created_at,t.rolled_back,t.requested_at,t.turn_order
 FROM conversation_activities a JOIN owned t ON a.turn_id=t.id AND a.conversation_id=t.conversation_id
)
SELECT json_object('sequence',sequence,'kind',substr(kind,1,64),'role',substr(role,1,64),
'state',substr(state,1,64),'turnState',substr(turn_state,1,64),
'text',CAST(substr(CAST(COALESCE(text,'') AS BLOB),1,2048) AS TEXT),
'detail',CAST(substr(CAST(COALESCE(detail,'') AS BLOB),1,1024) AS TEXT),
'output',CAST(substr(CAST(COALESCE(output,'') AS BLOB),1,1024) AS TEXT),
'streamedText',CAST(substr(CAST(COALESCE(streamed,'') AS BLOB),1,512) AS TEXT),
'createdAt',substr(created_at,1,64),'rolledBack',json(CASE WHEN rolled_back THEN 'true' ELSE 'false' END))
FROM timeline ORDER BY requested_at DESC,turn_order DESC,sequence DESC LIMIT 6`},
		{"provider-archive", `SELECT json_object('method',substr(method,1,128),
'payload',CAST(substr(CAST(payload_json AS BLOB),1,2048) AS TEXT),'receivedAt',substr(received_at,1,64))
FROM conversation_provider_events WHERE session_id=? ORDER BY id DESC LIMIT 4`},
	}
	if id == "" {
		queries = []struct{ kind, sql string }{{"session-candidates", `SELECT json_object('id',id,'label',substr(display_name,1,240),'harness',substr(harness,1,64),
'state',CASE WHEN is_terminated THEN 'terminated' ELSE substr(activity_state,1,64) END,'updatedAt',substr(updated_at,1,64))
FROM sessions WHERE length(CAST(id AS BLOB))<=128 AND ?='' ORDER BY updated_at DESC,id DESC LIMIT 8`}}
	}
	parts := make([]TriageDiagnostic, 0, len(queries))
	for _, query := range queries {
		entries, err := s.readTriageJSON(ctx, query.sql, id)
		parts = append(parts, TriageDiagnostic{Kind: query.kind, Entries: entries, Err: err})
	}
	return parts
}

func (s *Store) readTriageJSON(ctx context.Context, query string, id domain.SessionID) ([]json.RawMessage, error) {
	rows, err := s.readDB.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var entries []json.RawMessage
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		entries = append(entries, json.RawMessage(raw))
	}
	return entries, rows.Err()
}

// ReadTriageWorkspaceRoots returns at most eight complete registered paths.
// Oversized paths are excluded rather than turned into operational inputs.
func (s *Store) ReadTriageWorkspaceRoots(ctx context.Context, id domain.SessionID) ([]TriageWorkspaceRoot, error) {
	entries, err := s.readTriageJSON(ctx, `SELECT json_object('Name',repo_name,'Path',worktree_path)
FROM session_worktrees WHERE session_id=? AND state='active'
AND EXISTS (SELECT 1 FROM sessions WHERE id=session_worktrees.session_id)
AND length(CAST(repo_name AS BLOB))<=128 AND length(CAST(worktree_path AS BLOB))<=4096
ORDER BY CASE WHEN repo_name='__root__' THEN 0 ELSE 1 END,repo_name LIMIT 8`, id)
	if err != nil {
		return nil, err
	}
	roots := make([]TriageWorkspaceRoot, 0, len(entries))
	for _, entry := range entries {
		var root TriageWorkspaceRoot
		if err := json.Unmarshal(entry, &root); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, nil
}
