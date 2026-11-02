package sqlite

import (
	"strings"
	"testing"
)

// An unreleased dev build of card summaries left a trigger with the same name
// on latest_assistant_update. The migration must replace it rather than fail,
// which would stop the daemon from starting on those databases.
func TestCardSummaryMigrationReplacesTheDevBuildTrigger(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261101173946025)
	if _, err := db.Exec(`CREATE TRIGGER sessions_card_summary_cdc
AFTER UPDATE OF latest_assistant_update ON sessions
WHEN NEW.latest_assistant_update <> OLD.latest_assistant_update
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated', json_object('id', NEW.id), NEW.updated_at);
END`); err != nil {
		t.Fatalf("seed dev-build trigger: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate over the dev-build trigger: %v", err)
	}
	var trigger string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'sessions_card_summary_cdc'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trigger, "AFTER UPDATE OF card_summary") {
		t.Fatalf("trigger still watches the old column: %s", trigger)
	}
}
