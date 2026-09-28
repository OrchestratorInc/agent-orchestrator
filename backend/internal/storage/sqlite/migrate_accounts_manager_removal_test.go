package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestMigration0172RetainsDeletionProof(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 171)
	now := time.Now().UTC()
	mustExec(t, db, `INSERT INTO accounts_manager_removals(id,account_id,impact,phase,error_code,created_at,updated_at) VALUES ('pending','account-a','{"Revision":1,"Sessions":[]}','stopping','',?,?)`, now, now)
	upTo(t, db, 172)
	var started, revoked int
	var phase string
	if err := db.QueryRow(`SELECT phase,stop_started,bindings_revoked FROM accounts_manager_removals WHERE id='pending'`).Scan(&phase, &started, &revoked); err != nil {
		t.Fatal(err)
	}
	if phase != "stopping" || started != 1 || revoked != 0 {
		t.Fatal("migration fabricated cancellation or revocation proof")
	}
	gooseMu.Lock()
	err := goose.DownToContext(context.Background(), db, "migrations", 171)
	gooseMu.Unlock()
	if err == nil {
		t.Fatal("downgrade discarded irreversible deletion obligations")
	}
	if err := db.QueryRow(`SELECT stop_started FROM accounts_manager_removals WHERE id='pending'`).Scan(&started); err != nil || started != 1 {
		t.Fatal("failed downgrade damaged deletion proof", err)
	}
}
