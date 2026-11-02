package sqlite

import (
	"database/sql"
	"testing"
	"time"
)

// Older builds left a Chat orchestrator bound to both its provisioning-time
// session row and its project row (#6523). 0197 detaches the session row once
// the orchestrator has run, keeps it for one still waiting for its first
// controller (whose start moves the queue itself), and leaves workers alone.
func TestMigration0197DetachesSplitOrchestratorConversations(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 196)
	now := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	mustExec(t, db, `
INSERT INTO projects (id, path, display_name, registered_at) VALUES
    ('ran', '/tmp/ran', 'Ran', ?),
    ('pending', '/tmp/pending', 'Pending', ?);
INSERT INTO sessions (
    id, project_id, num, kind, harness, session_mode, provision_state,
    controller_generation, activity_last_at, created_at, updated_at
) VALUES
    ('ran-orchestrator', 'ran', 1, 'orchestrator', 'codex', 'chat', 'ready', 'gen-1', ?, ?, ?),
    ('pending-orchestrator', 'pending', 1, 'orchestrator', 'codex', 'chat', 'provisioning', '', ?, ?, ?),
    ('worker', 'ran', 2, 'worker', 'codex', 'chat', 'ready', 'gen-2', ?, ?, ?);
INSERT INTO conversations (
    id, scope, project_id, session_id, current_session_id, active_branch_id, created_at, updated_at
) VALUES
    ('ran-provisioning', 'session', 'ran', 'ran-orchestrator', 'ran-orchestrator', 'ran-provisioning:root', ?, ?),
    ('ran-project', 'project', 'ran', NULL, 'ran-orchestrator', 'ran-project:root', ?, ?),
    ('pending-provisioning', 'session', 'pending', 'pending-orchestrator', 'pending-orchestrator', 'pending-provisioning:root', ?, ?),
    ('pending-project', 'project', 'pending', NULL, 'pending-orchestrator', 'pending-project:root', ?, ?),
    ('worker-conversation', 'session', 'ran', 'worker', 'worker', 'worker-conversation:root', ?, ?);`,
		now, now,
		now, now, now, now, now, now, now, now, now,
		now, now, now, now, now, now, now, now, now, now)

	upTo(t, db, 197)

	for id, want := range map[string]string{
		"ran-provisioning":     "",
		"ran-project":          "ran-orchestrator",
		"pending-provisioning": "pending-orchestrator",
		"pending-project":      "pending-orchestrator",
		"worker-conversation":  "worker",
	} {
		var current sql.NullString
		if err := db.QueryRow(`SELECT current_session_id FROM conversations WHERE id = ?`, id).Scan(&current); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if current.String != want {
			t.Errorf("%s current_session_id = %q, want %q", id, current.String, want)
		}
	}
}
