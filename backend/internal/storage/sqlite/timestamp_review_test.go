package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimestampCorruptLedgerStillRepairs(t *testing.T) {
	for name, data := range map[string]string{"empty": "", "garbage": "{broken", "null": "null"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('ledger',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 IST')`); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "timestamp-repair-skips.json"), []byte(data), 0o600); err != nil {
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
				t.Fatalf("corrupt ledger stopped repair: %q %v", raw, err)
			}
		})
	}
}

type repairScanCounts struct{ connections, writer, reader atomic.Int64 }
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
	if strings.Contains(query, "GLOB") && (strings.HasPrefix(query, "SELECT rowid,") || strings.HasPrefix(query, "SELECT min(rowid),max(rowid)")) {
		if c.writer {
			c.counts.writer.Add(1)
		} else {
			c.counts.reader.Add(1)
		}
	}
	return c.sqliteConn.QueryContext(ctx, query, args)
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
func TestTimestampCompletedOpenDoesNotScan(t *testing.T) {
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
	if n := second.writer.Load() + second.reader.Load(); n != 0 {
		t.Fatalf("completed database rescanned on second Open: %d", n)
	}
}

func TestTimestampOldWriterWithoutCDCDefersRescan(t *testing.T) {
	for _, whileOpen := range []bool{false, true} {
		t.Run(fmt.Sprint(whileOpen), func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('cleanup',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
  INSERT INTO session_cleanup_facts(session_id,session_generation,workspace_disposition,next_attempt_at) VALUES('cleanup',0,'pending','2026-10-21 03:30:00 +0000 UTC')`); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			s, err := openWithTimestampOptions(dir, timestampOpenOptions{fenceBudget: 30 * time.Second, repairDone: done})
			if err != nil {
				t.Fatal(err)
			}
			<-done
			if !whileOpen {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			old, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
			if err != nil {
				t.Fatal(err)
			}
			watermark := func() [2]int64 {
				var value [2]int64
				if err := old.QueryRow(`SELECT coalesce((SELECT max(seq) FROM change_log),0),(SELECT max(rowid) FROM session_cleanup_facts)`).Scan(&value[0], &value[1]); err != nil {
					t.Fatal(err)
				}
				return value
			}
			before := watermark()
			if _, err := old.Exec(`UPDATE session_cleanup_facts SET next_attempt_at='2026-10-21 09:00:00 +0530 IST' WHERE session_id='cleanup'`); err != nil {
				t.Fatal(err)
			}
			if after := watermark(); after != before {
				t.Fatalf("fixture unexpectedly moved log/row watermark: %v -> %v", before, after)
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			if whileOpen {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			var logs bytes.Buffer
			original := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(original)
			done = make(chan struct{})
			counts := &repairScanCounts{}
			s, err = openWithTimestampOptions(dir, repairScanOptions(t, counts, done))
			if err != nil {
				t.Fatal(err)
			}
			<-done
			facts, found, err := s.GetSessionCleanupFacts(context.Background(), "cleanup")
			if err != nil || !found || !facts.NextAttemptAt.Equal(time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)) {
				t.Fatalf("old-writer due time not repaired: %+v %v %v", facts, found, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(logs.String(), "timestamp startup fence deferred") {
				t.Fatalf("old-writer rescan was not deferred: %s", logs.String())
			}
			if counts.reader.Load() == 0 || counts.writer.Load() != 0 {
				t.Fatalf("rescan pools: reader=%d writer=%d", counts.reader.Load(), counts.writer.Load())
			}
		})
	}
}
