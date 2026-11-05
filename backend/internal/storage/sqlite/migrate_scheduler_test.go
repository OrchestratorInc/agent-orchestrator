package sqlite

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// 删除 Down 清理或 Up 的原子建表会使往返迁移留下残表、事件或损坏约束。
func TestM3SchedulerMigrationRoundTrip(t *testing.T) {
	const previous int64 = 20261104170421478
	db := openMigratedDatabaseCopy(t, previous)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO scheduler_policy_changes VALUES('m3-change','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','resource','global','{}','test','TEST','2026-11-05 00:00:00+00:00')`)
	if err := goose.DownTo(db, "migrations", previous); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND (name LIKE 'scheduler_%' OR name LIKE 'budget_%' OR name LIKE 'workflow_usage_%')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("remaining tables=%d err=%v", count, err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%s err=%v", integrity, err)
	}
	var cap int
	if err := db.QueryRow(`SELECT max_concurrent FROM scheduler_policies WHERE id='global'`).Scan(&cap); err != nil || cap != 3 {
		t.Fatalf("usable default after reapply=%d err=%v", cap, err)
	}
}
