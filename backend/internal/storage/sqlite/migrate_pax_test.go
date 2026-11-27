package sqlite

import (
	"strings"
	"testing"
)

// paxBaseline is main's latest migration before Pax's.
const paxBaseline int64 = 20261126171749354

func TestPaxMigrationRoundTripFromCurrentMain(t *testing.T) {
	db := openMigratedDatabaseCopy(t, paxBaseline)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "'pax'") {
		t.Fatal("pending Pax migration ran before current main baseline")
	}
	mustExec(t, db, `INSERT INTO projects(id,path,registered_at) VALUES ('pax-updown','/tmp/pax-updown',CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions(id,project_id,num,harness,created_at,updated_at,activity_last_at) VALUES (?,'pax-updown',?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
	upTo(t, db, 20261127120000000)
	mustExec(t, db, insert, "native", 1, "pax")
	mustExec(t, db, insert, "main-mastra", 2, "mastra-code")
	if _, err := db.Exec(insert, "unknown", 3, "unknown-pax"); err == nil {
		t.Fatal("unknown harness bypassed CHECK")
	}
	mustExec(t, db, `UPDATE sessions SET harness='' WHERE harness='pax'`)
	downTo(t, db, paxBaseline)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration changed prior schema: %v", err)
	}
	if _, err := db.Exec(insert, "after-down", 4, "pax"); err == nil {
		t.Fatal("downgrade still allows Pax")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %s, %v", integrity, err)
	}
}

// Down runs without a transaction. If it stops after the schema rewrite, the
// version stays applied with Pax already gone; a retried Down must finish
// instead of rejecting that restored constraint.
func TestPaxMigrationDownResumesAfterInterruptedRewrite(t *testing.T) {
	db := openMigratedDatabaseCopy(t, paxBaseline)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	upTo(t, db, 20261127120000000)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''pax'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	downTo(t, db, paxBaseline)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("resumed down left schema %q, %v; want the prior schema", after, err)
	}
}

func TestReconcileRestoresPaxAfterMastraCode(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261127120000000)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''pax'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatal(err)
	}
	var repaired string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&repaired); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repaired, "'mastra-code', 'pax', 'fake'))") {
		t.Fatalf("Pax is not in fresh-database order after repair: %s", repaired)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %s, %v", integrity, err)
	}
}
