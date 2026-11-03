package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func TestRepairNormalizesReadableLocalComparisons(t *testing.T) {
	db := openMigratedTestDB(t)
	const local = "2026-10-21 18:15:40.123456789 +0200 CEST"
	_, err := db.Exec(`INSERT INTO sessions (id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at,latest_user_prompt,latest_user_prompt_at) VALUES ('cest',1,'aider','idle',?,0,?,?, 'earlier',?)`, local, local, local, local)
	if err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 10, 21, 16, 30, 0, 0, time.UTC)
	changed, err := gen.New(db).RecordSessionHumanMessage(context.Background(), gen.RecordSessionHumanMessageParams{ID: "cest", LatestUserPrompt: "later", LatestUserPromptAt: sql.NullTime{Time: later, Valid: true}})
	if err != nil || changed != 1 {
		t.Fatalf("later UTC prompt changed %d rows, want 1: %v", changed, err)
	}
	var got time.Time
	if err := db.QueryRow(`SELECT created_at FROM sessions WHERE id='cest'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOpenServesWhenTimestampRepairCannotWrite(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	_, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES ('bad','/tmp/bad','2026-10-21 18:15:40 +0200 +0200'); CREATE TABLE aaa_repair(at TIMESTAMP); INSERT INTO aaa_repair VALUES ('2026-10-21 18:15:40 +0200 +0200'); CREATE TRIGGER block_repair BEFORE UPDATE ON aaa_repair BEGIN SELECT RAISE(ABORT,'repair blocked'); END; PRAGMA user_version=0;`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("repair blocked storage startup: %v", err)
	}
	defer s.Close()
	projects, err := s.ListProjects(context.Background())
	if err != nil || len(projects) != 1 {
		t.Fatalf("list during repair: %d projects, %v", len(projects), err)
	}
	// Read-only readers need the same fallback, without altering stored bytes.
	ro, err := OpenReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err = ro.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = db.QueryRow(`SELECT CAST(registered_at AS TEXT) FROM projects`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "2026-10-21 18:15:40 +0200 +0200" {
		t.Fatalf("blocked repair changed bytes: %q", raw)
	}
}

func TestRepairHundredThousandDistinctTimes(t *testing.T) {
	db := openRepairDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO repair_times(id,at) VALUES (?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < 100001; i++ {
		if _, err := stmt.Exec(i+1, fmt.Sprintf("2026-10-21 18:15:40.%09d +0200 +0200", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Logf("100001 distinct timestamps repaired in %s", time.Since(start))
	var bad int
	if err := db.QueryRow(`SELECT count(*) FROM repair_times WHERE CAST(at AS TEXT) LIKE '%+0200 +0200%'`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("%d bad values remain: %v", bad, err)
	}
}

// Clean rows must consume the detection budget too. LIMIT after the timestamp
// predicate can otherwise hold the sole writer for a full clean-table scan.
func TestTimestampDetectionPagesLargeCleanTables(t *testing.T) {
	db := openRepairDB(t)
	if _, err := db.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<100001)
 INSERT INTO repair_times(id,at) SELECT i,'2026-10-21 16:15:40 +0000 UTC' FROM ids`); err != nil {
		t.Fatal(err)
	}
	column := timestampColumn{table: "repair_times", column: "at"}
	next, done, err := repairTimestampBatch(context.Background(), db, []timestampColumn{column}, -1<<63)
	if err != nil {
		t.Fatal(err)
	}
	if done || next != 257 {
		t.Fatalf("first clean batch returned next=%d done=%v, want next=257 done=false", next, done)
	}
	// A sparse bad row later in the table must still be reached after clean pages.
	if _, err := db.Exec(`UPDATE repair_times SET at='2026-10-21 18:15:40 +0200 +0200' WHERE id=100000`); err != nil {
		t.Fatal(err)
	}
	for !done {
		previous := next
		next, done, err = repairTimestampBatch(context.Background(), db, []timestampColumn{column}, next)
		if err != nil {
			t.Fatal(err)
		}
		if !done && next-previous != 256 {
			t.Fatalf("batch examined more than its row budget: %d -> %d", previous, next)
		}
	}
	var at time.Time
	if err := db.QueryRow(`SELECT at FROM repair_times WHERE id=100000`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 21, 16, 15, 40, 0, time.UTC); !at.Equal(want) {
		t.Fatalf("sparse repair: %v, want %v", at, want)
	}
}

func TestTimestampRepairCancellationCanRetry(t *testing.T) {
	db := openRepairDB(t)
	if _, err := db.Exec(`INSERT INTO repair_times(id,at) VALUES(1,'2026-10-21 18:15:40 +0200 +0200')`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := repairTimestamps(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled repair: %v", err)
	}
	if err := canScanTime(db, `SELECT at FROM repair_times WHERE id=1`); err == nil {
		t.Fatal("cancelled repair changed the row")
	}
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := canScanTime(db, `SELECT at FROM repair_times WHERE id=1`); err != nil {
		t.Fatal(err)
	}
}

func TestTimestampRepairCloseCancelsWorker(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started, finished := make(chan struct{}), make(chan struct{})
	s.StartTimestampRepair(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	})
	<-started
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Close returned before the repair worker finished")
	}
}

func TestOpenMessageBeforeSessionTimestampRepair(t *testing.T) {
	for name, legacy := range map[string]string{
		"named":           "2026-10-21 18:15:40.123456789 +0200 CEST",
		"numeric":         "2026-10-21 18:15:40.123456789 +0200 +0200",
		"rfc3339":         "2026-10-21T18:15:40.123456789+02:00",
		"negative":        "2026-10-21 12:15:40.123456789 -0400 EDT",
		"monotonic":       "2026-10-21 18:15:40.123456789 +0200 CEST m=+1.234",
		"later lifecycle": "2026-10-21 18:15:40.123456789 +0200 CEST",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			const good = "2026-10-21 16:15:40.123456789 +0000 UTC"
			updated := legacy
			wantUpdated := time.Date(2026, 10, 21, 16, 30, 0, 0, time.UTC)
			if name == "later lifecycle" {
				updated = "2026-10-21 13:45:40.123456789 -0400 EDT"
				wantUpdated = time.Date(2026, 10, 21, 17, 45, 40, 123456789, time.UTC)
			}
			_, err := db.Exec(`INSERT INTO sessions(id,num,harness,session_mode,controller_generation,activity_state,activity_last_at,is_terminated,created_at,updated_at,latest_user_prompt,latest_user_prompt_at,latest_assistant_update,conversation_checkpoint_state,conversation_checkpoint_generation,conversation_checkpoint_native_id) VALUES('pending',1,'aider','chat','gen-1','idle',?,0,?,?,'earlier',?,'old answer','complete','old-gen','old-native');
INSERT INTO conversations(id,scope,session_id,current_session_id,created_at,updated_at) VALUES('pending-conv','session','pending','pending',?,?);
CREATE TABLE aaa_repair(at TIMESTAMP);
INSERT INTO aaa_repair VALUES('2026-10-21 18:15:40 +0200 +0200');
CREATE TRIGGER block_repair BEFORE UPDATE ON aaa_repair BEGIN SELECT RAISE(ABORT,'repair cannot reach sessions'); END;`, good, good, updated, legacy, good, good)
			if err != nil {
				t.Fatal(err)
			}
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var before string
			if err := db.QueryRow(`SELECT CAST(latest_user_prompt_at AS TEXT) FROM sessions WHERE id='pending'`).Scan(&before); err != nil || before != legacy {
				t.Fatalf("fixture was repaired before send: %q %v", before, err)
			}
			later := time.Date(2026, 10, 21, 16, 30, 0, 0, time.UTC)
			created, err := s.AppendUserMessage(context.Background(), "pending-conv", "pending", "gen-1", domain.ConversationMessage{ID: "later-message", Text: "later UTC prompt", Origin: domain.MessageOriginHuman}, "later-turn", later)
			if err != nil || !created {
				t.Fatalf("send: created=%v err=%v", created, err)
			}
			got, ok, err := s.GetSession(context.Background(), "pending")
			if err != nil || !ok {
				t.Fatalf("read sent facts: ok=%v %v", ok, err)
			}
			if got.Metadata.LatestUserPrompt != "later UTC prompt" || !got.Metadata.LatestUserPromptAt.Equal(later) || !got.UpdatedAt.Equal(wantUpdated) {
				t.Fatalf("send succeeded with stale prompt facts: %+v", got.Metadata)
			}
			if got.Metadata.LatestAssistantUpdate != "" || got.Metadata.ConversationCheckpointState != domain.ConversationCheckpointLegacy || got.Metadata.ConversationCheckpointGeneration != "" || got.Metadata.ConversationCheckpointNativeID != "" {
				t.Fatalf("send kept stale checkpoint: %+v", got.Metadata)
			}
			for _, table := range []string{"conversation_messages", "conversation_turns"} {
				var count int
				if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 1 {
					t.Fatalf("send persisted %d %s: %v", count, table, err)
				}
			}
			if _, err := db.Exec(`DROP TRIGGER block_repair`); err != nil {
				t.Fatal(err)
			}
			if err := repairTimestamps(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			after, _, err := s.GetSession(context.Background(), "pending")
			if err != nil || after.Metadata.LatestUserPrompt != got.Metadata.LatestUserPrompt || !after.Metadata.LatestUserPromptAt.Equal(later) {
				t.Fatalf("background completion changed sent facts: %+v %v", after.Metadata, err)
			}
		})
	}
}

// The blocker makes pending repair deterministic without holding the writer.
func pendingSessionGuardFixture(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	const good = "2026-10-21 16:15:40.123456789 +0000 UTC"
	const legacy = "2026-10-21 18:15:40.123456789 +0200 CEST"
	_, err := db.Exec(`INSERT INTO sessions(id,num,harness,session_mode,controller_generation,activity_state,activity_last_at,is_terminated,created_at,updated_at,latest_user_prompt,latest_user_prompt_at,latest_interaction_at)
VALUES('pending',1,'aider','chat','gen-1','idle',?,0,?,?,'earlier',?,?);
INSERT INTO conversations(id,scope,session_id,current_session_id,created_at,updated_at) VALUES('pending-conv','session','pending','pending',?,?);
CREATE TABLE aaa_repair(at TIMESTAMP);
INSERT INTO aaa_repair VALUES('2026-10-21 18:15:40 +0200 +0200');
CREATE TRIGGER block_repair BEFORE UPDATE ON aaa_repair BEGIN SELECT RAISE(ABORT,'repair cannot reach sessions'); END;`, good, good, legacy, legacy, legacy, good, good)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, db
}

func TestPendingSessionTimestampGuardBoundaries(t *testing.T) {
	old := time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)
	for _, delta := range []time.Duration{-1, 0, 1} {
		t.Run(delta.String(), func(t *testing.T) {
			s, _ := pendingSessionGuardFixture(t)
			ctx := context.Background()
			at := old.Add(delta)
			changed, err := s.RecordSessionLatestUserPrompt(ctx, "pending", "pane prompt", at)
			if err != nil || changed != (delta >= 0) {
				t.Fatalf("pane guard: changed=%v err=%v", changed, err)
			}
			if err := s.RecordSessionInteraction(ctx, "pending", "", at); err != nil {
				t.Fatal(err)
			}
			got, _, err := s.GetSession(ctx, "pending")
			if err != nil {
				t.Fatal(err)
			}
			want := old
			if delta > 0 {
				want = at
			}
			if !got.Metadata.LatestInteractionAt.Equal(want) || !got.UpdatedAt.Equal(want) {
				t.Fatalf("interaction guard lost instant: %+v", got)
			}
			// An equal interaction and an older prompt must not write clean rows.
			rev := got.Revision
			if err := s.RecordSessionInteraction(ctx, "pending", "", want); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordSessionLatestUserPrompt(ctx, "pending", "stale", old.Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			got, _, err = s.GetSession(ctx, "pending")
			if err != nil || got.Revision != rev {
				t.Fatalf("clean/replayed write changed revision: %d -> %d %v", rev, got.Revision, err)
			}
		})
	}
}

func TestPendingSessionNormalizationRollsBackWithSend(t *testing.T) {
	s, db := pendingSessionGuardFixture(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_send BEFORE INSERT ON conversation_messages BEGIN SELECT RAISE(ABORT,'send failed'); END;`); err != nil {
		t.Fatal(err)
	}
	_, err := s.AppendUserMessage(context.Background(), "pending-conv", "pending", "gen-1", domain.ConversationMessage{ID: "failed-message", Text: "failed prompt", Origin: domain.MessageOriginHuman}, "failed-turn", time.Date(2026, 10, 21, 16, 30, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("send unexpectedly succeeded")
	}
	var raw, prompt string
	if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT),latest_user_prompt FROM sessions WHERE id='pending'`).Scan(&raw, &prompt); err != nil {
		t.Fatal(err)
	}
	if raw != "2026-10-21 18:15:40.123456789 +0200 CEST" || prompt != "earlier" {
		t.Fatalf("failed send committed normalization: %q %q", raw, prompt)
	}
	var turns int
	if err := db.QueryRow(`SELECT count(*) FROM conversation_turns`).Scan(&turns); err != nil || turns != 0 {
		t.Fatalf("failed send left %d turns: %v", turns, err)
	}
}

func TestPendingSteerNormalizationReusesProjectionTransaction(t *testing.T) {
	s, db := pendingSessionGuardFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	at := time.Date(2026, 10, 21, 16, 30, 0, 0, time.UTC)
	rollback := errors.New("roll back projection")
	project := func(fail bool) error {
		_, err := s.ProjectProviderEvent(ctx, "pending-conv", "pending", "gen-1", "steer-event", "activity.started", `{}`, at, func(txCtx context.Context) error {
			err := s.UpsertActivity(txCtx, "pending-conv", "", domain.ConversationActivity{ID: "steer", Kind: domain.ActivityKindAOAction, Status: domain.ActivityStatusCompleted, Detail: []byte(`{"event":"steer","origin":"human"}`)}, at)
			if err != nil {
				return err
			}
			if fail {
				return rollback
			}
			return nil
		})
		return err
	}
	if err := project(true); !errors.Is(err, rollback) {
		t.Fatalf("projection rollback: %v", err)
	}
	var raw string
	if err := db.QueryRow(`SELECT CAST(latest_interaction_at AS TEXT) FROM sessions WHERE id='pending'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "2026-10-21 18:15:40.123456789 +0200 CEST" {
		t.Fatalf("projection committed normalization: %q", raw)
	}
	if err := project(false); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GetSession(ctx, "pending")
	if err != nil || !got.Metadata.LatestInteractionAt.Equal(at) {
		t.Fatalf("steer did not advance recency: %+v %v", got.Metadata, err)
	}
}

func TestRepairConstrainedTimestampRowsAtomically(t *testing.T) {
	for _, table := range []string{"agent_native_sessions", "agent_switches"} {
		t.Run(table, func(t *testing.T) {
			db := openMigratedTestDB(t)
			const legacy = "2026-10-21 12:00:00.123456789 -0400 EDT"
			_, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('constrained',1,'aider','idle',?,0,?,?);
INSERT INTO projects(id,path,registered_at) VALUES('after-constrained','/tmp/constrained',?);`, legacy, legacy, legacy, legacy)
			if err != nil {
				t.Fatal(err)
			}
			var columns []string
			if table == "agent_native_sessions" {
				columns = []string{"created_at", "last_used_at"}
				_, err = db.Exec(`INSERT INTO agent_native_sessions(id,ao_session_id,harness,created_at,last_used_at) VALUES('legacy','constrained','aider',?,?)`, legacy, legacy)
			} else {
				columns = []string{"target_acknowledged_at", "requested_at", "updated_at"}
				_, err = db.Exec(`INSERT INTO agent_switches(id,session_id,idempotency_key,request_fingerprint,from_harness,target_harness,source_generation_id,target_generation_id,target_acknowledged_at,requested_at,updated_at) VALUES('legacy','constrained','key',?,'aider','codex','source','target',?,?,?)`, "v1:"+strings.Repeat("0", 64), legacy, legacy, legacy)
			}
			if err != nil {
				t.Fatalf("valid legacy fixture: %v", err)
			}
			for range 2 {
				if err := repairTimestamps(context.Background(), db); err != nil {
					t.Fatal(err)
				}
				for _, column := range columns {
					var raw string
					if err := db.QueryRow("SELECT CAST(" + column + " AS TEXT) FROM " + table + " WHERE id='legacy'").Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if raw != "2026-10-21 16:00:00.123456789 +0000 UTC" {
						t.Fatalf("%s.%s left %q", table, column, raw)
					}
				}
			}
			var raw, integrity string
			if err := db.QueryRow(`SELECT CAST(registered_at AS TEXT) FROM projects WHERE id='after-constrained'`).Scan(&raw); err != nil || raw != "2026-10-21 16:00:00.123456789 +0000 UTC" {
				t.Fatalf("repair stopped before projects: %q %v", raw, err)
			}
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity: %q %v", integrity, err)
			}
		})
	}
}
