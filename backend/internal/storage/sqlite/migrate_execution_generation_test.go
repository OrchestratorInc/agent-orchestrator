package sqlite

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// 删除部分唯一索引、ON DELETE SET NULL 或重建复制任一项，会破坏执行历史、预算外键或占位唯一性。
func TestM3ExecutionGenerationMigrationPreservesHistoryAndForeignKeys(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261105094026402)
	mustExec(t, db, `INSERT INTO sessions(id,num,harness,activity_last_at,created_at,updated_at) VALUES('generation-session',1,'codex',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO scheduler_admissions(id,session_id,harness,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at) VALUES('old','generation-session','codex','old-request','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','test',1,'active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO budget_policies(id,scope,scope_id,currency,mode,period,limit_nanos,revision) VALUES('budget','global','','USD','observe','lifetime',100,1)`)
	mustExec(t, db, `INSERT INTO budget_reservations(admission_id,policy_id,window,basis,reference,state,revision) VALUES('old','budget','lifetime','','','reserved',1)`)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE scheduler_admissions SET state='released',revision=revision+1 WHERE id='old'`)
	mustExec(t, db, `INSERT INTO scheduler_admissions(id,session_id,harness,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at,controller_generation) VALUES('new','generation-session','codex','new-request','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','test',1,'active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'generation-2')`)
	if _, err := db.Exec(`INSERT INTO scheduler_admissions(id,session_id,harness,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at) VALUES('duplicate','generation-session','codex','duplicate','cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc','test',1,'active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("two occupied admissions accepted for one session")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM budget_reservations WHERE admission_id='old'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("budget reference lost: %d %v", count, err)
	}
	// 不兼容 Down 必须在事务外拒绝，否则 NO TRANSACTION 的失败会留下半开的重建事务。
	if err := goose.DownTo(db, "migrations", 20261105094026402); err == nil {
		t.Fatal("downgrade discarded repeated session history")
	}
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed downgrade disabled foreign keys: %d %v", count, err)
	}
	mustExec(t, db, `BEGIN IMMEDIATE`)
	mustExec(t, db, `ROLLBACK`)
	mustExec(t, db, `DELETE FROM change_log WHERE session_id='generation-session'`)
	mustExec(t, db, `DELETE FROM sessions WHERE id='generation-session'`)
	if err := db.QueryRow(`SELECT count(*) FROM scheduler_admissions WHERE session_id IS NULL`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("history deleted: %d %v", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM change_log WHERE event_type='scheduler_updated' AND json_extract(payload,'$.id')='new'`).Scan(&count); err != nil || count == 0 {
		t.Fatalf("admission CDC missing: %d %v", count, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration left invalid foreign keys")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
