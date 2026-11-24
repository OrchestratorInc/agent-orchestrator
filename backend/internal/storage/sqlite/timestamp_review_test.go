package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestTimestampObsoleteAuditSidecarDoesNotBlockOpen(t *testing.T) {
	for name, data := range map[string]string{
		"empty": "", "garbage": "{broken", "null": "null",
		"stale failure": `{"sessions/1":"obsolete-failure-hash"}`,
		"directory":     "", "unreadable": "obsolete audit data",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('ledger',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 IST')`); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "timestamp-repair-skips.json")
			if name == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
				if name == "unreadable" {
					if err := os.Chmod(path, 0); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
				}
			}
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			var raw string
			if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions`).Scan(&raw); err != nil || raw != "2026-10-21 03:30:00 +0000 UTC" {
				t.Fatalf("obsolete audit sidecar stopped repair: %q %v", raw, err)
			}
		})
	}
}

type repairScanCounts struct {
	connections, writer, reader               atomic.Int64
	excludeTable                              string
	expireAfterBatch, sessionBatch, committed atomic.Bool
	fenceRows                                 atomic.Int64
	checkpointOutcome                         string
	checkpointCalls                           atomic.Int64
}
type repairScanDriver struct {
	driver.Driver
	counts *repairScanCounts
}
type repairScanConn struct {
	sqliteConn
	counts *repairScanCounts
	writer bool
}

var repairScanDriverID atomic.Int64

func (d repairScanDriver) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	conn, ok := c.(sqliteConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("incompatible test connection")
	}
	return repairScanConn{sqliteConn: conn, counts: d.counts, writer: d.counts.connections.Add(1) == 1}, nil
}
func (c repairScanConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if query == "PRAGMA wal_checkpoint(FULL)" {
		c.counts.checkpointCalls.Add(1)
		switch c.counts.checkpointOutcome {
		case "busy":
			return c.sqliteConn.QueryContext(ctx, "SELECT 1, 1, 0", nil)
		case "error":
			return nil, errors.New("injected checkpoint failure")
		}
	}

	if c.counts.committed.Load() && strings.Contains(query, `FROM "sessions" `) && strings.HasPrefix(query, "SELECT rowid,") && c.counts.expireAfterBatch.CompareAndSwap(true, false) {
		rows, err := c.sqliteConn.QueryContext(ctx, `SELECT count(*) FROM sessions WHERE CAST(created_at AS TEXT) GLOB '* +0000 UTC'`, nil)
		if err != nil {
			return nil, err
		}
		values := make([]driver.Value, 1)
		if err := rows.Next(values); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		n, ok := values[0].(int64)
		if !ok {
			return nil, fmt.Errorf("unexpected repaired row count %T", values[0])
		}
		c.counts.fenceRows.Store(n)
		return nil, context.DeadlineExceeded
	}
	if strings.Contains(query, "GLOB") && (strings.HasPrefix(query, "SELECT rowid,") || strings.HasPrefix(query, "SELECT min(rowid),max(rowid)")) &&
		(c.counts.excludeTable == "" || !strings.Contains(query, " FROM "+quoteIdentifier(c.counts.excludeTable)+" ")) {
		if c.writer {
			c.counts.writer.Add(1)
		} else {
			c.counts.reader.Add(1)
		}
	}
	return c.sqliteConn.QueryContext(ctx, query, args)
}
func (c repairScanConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if strings.HasPrefix(query, `UPDATE "sessions" SET`) {
		c.counts.sessionBatch.Store(true)
	}
	return c.sqliteConn.PrepareContext(ctx, query)
}
func (c repairScanConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	result, err := c.sqliteConn.ExecContext(ctx, query, args)
	if err == nil && query == "COMMIT" && c.counts.sessionBatch.Swap(false) {
		c.counts.committed.Store(true)
	}
	return result, err
}

func repairScanOptions(t *testing.T, counts *repairScanCounts, done chan<- struct{}) timestampOpenOptions {
	t.Helper()
	base, err := sql.Open("ao-sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	name := fmt.Sprintf("repair-scan-%d", repairScanDriverID.Add(1))
	sql.Register(name, repairScanDriver{Driver: base.Driver(), counts: counts})
	return timestampOpenOptions{driver: name, fenceBudget: 30 * time.Second, repairDone: done}
}
func TestTimestampReopenChecksForOlderWriters(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('complete',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 IST')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	first, done := &repairScanCounts{}, make(chan struct{})
	s, err := openWithTimestampOptions(dir, repairScanOptions(t, first, done))
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	second, done := &repairScanCounts{}, make(chan struct{})
	s, err = openWithTimestampOptions(dir, repairScanOptions(t, second, done))
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := first.writer.Load(); n != 0 {
		t.Errorf("detection used the writer pool: %d scans", n)
	}
	if first.reader.Load() == 0 {
		t.Error("first pass did not scan on reader pool")
	}
	if second.writer.Load() != 0 || second.reader.Load() != 0 {
		t.Fatalf("clean reopen rescanned timestamps: writer=%d reader=%d", second.writer.Load(), second.reader.Load())
	}
	if second.committed.Load() {
		t.Fatal("clean reopen rewrote a session")
	}
}

func TestTimestampCloseProducesStandaloneDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: "checkpoint", Path: "/tmp/checkpoint", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir()
	if err := os.WriteFile(filepath.Join(clone, "ao.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	copied, err := OpenPreMigrated(clone)
	if err != nil {
		t.Fatalf("closed database still needs its WAL: %v", err)
	}
	defer copied.Close()
	if _, found, err := copied.GetProject(context.Background(), "checkpoint"); err != nil || !found {
		t.Fatalf("closed database lost recent facts: found=%v err=%v", found, err)
	}
}

func TestTimestampCASMissRetriesOnNextOpen(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('miss','/tmp/miss',CURRENT_TIMESTAMP); INSERT INTO conversations(id,scope,project_id,created_at,updated_at) VALUES('miss','project','miss','2026-10-21 09:00:00 +0530 IST','2026-10-21 09:00:00 +0530 IST');
 CREATE TRIGGER miss_timestamp_repair BEFORE UPDATE ON conversations BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	s, err := openWithTimestampOptions(dir, timestampOpenOptions{fenceBudget: 30 * time.Second, repairDone: done})
	if err == nil {
		_ = s.Close()
		t.Fatal("CAS miss published readers with unnormalized conversation times")
	}
	if _, err := db.Exec(`DROP TRIGGER miss_timestamp_repair`); err != nil {
		t.Fatal(err)
	}
	done = make(chan struct{})
	s, err = openWithTimestampOptions(dir, timestampOpenOptions{fenceBudget: 30 * time.Second, repairDone: done})
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var at string
	if err := db.QueryRow(`SELECT CAST(created_at AS TEXT) FROM conversations WHERE id='miss'`).Scan(&at); err != nil || at != "2026-10-21 03:30:00 +0000 UTC" {
		t.Fatalf("CAS miss was not retried: %q %v", at, err)
	}
}

func TestTimestampRepairDoesNotRequireCheckpoint(t *testing.T) {
	for _, outcome := range []string{"busy", "error"} {
		t.Run(outcome, func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('checkpoint',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 IST')`); err != nil {
				t.Fatal(err)
			}
			counts, done := &repairScanCounts{checkpointOutcome: outcome}, make(chan struct{})
			s, err := openWithTimestampOptions(dir, repairScanOptions(t, counts, done))
			if err != nil {
				t.Fatalf("checkpoint affected repair: %v", err)
			}
			<-done
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if counts.checkpointCalls.Load() != 0 {
				t.Fatal("repair attempted an unnecessary WAL checkpoint")
			}
			var raw string
			if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions`).Scan(&raw); err != nil || raw != "2026-10-21 03:30:00 +0000 UTC" {
				t.Fatalf("repair failed: %q %v", raw, err)
			}
		})
	}
}

func TestTimestampOldCompletionLedgerCannotHideLegacyRows(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('old-writer',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 +0530')`); err != nil {
		t.Fatal(err)
	}
	// This sidecar can survive a downgrade or replacing ao.db with a backup.
	if err := os.WriteFile(filepath.Join(dir, "timestamp-repair-skips.json"), []byte(`{"_repaired":"3","_complete3/sessions":"created_at\u0000updated_at\u0000"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions`).Scan(&raw); err != nil || raw != "2026-10-21 03:30:00 +0000 UTC" {
		t.Fatalf("stale completion hid legacy row: %q %v", raw, err)
	}
}
