package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	shelltermsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestSelectShellTerminalsBySessionID: Session Manager uses this to find and
// close every shell terminal scoped to a session before its worktree is torn
// down, so it must return only that session's rows.
func TestSelectShellTerminalsBySessionID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "portfolio")

	sessA, err := s.CreateSession(ctx, sampleRecord("portfolio"))
	if err != nil {
		t.Fatalf("create session a: %v", err)
	}
	sessB, err := s.CreateSession(ctx, sampleRecord("portfolio"))
	if err != nil {
		t.Fatalf("create session b: %v", err)
	}

	recA1 := shellTerminalRecord("shellterm-a1", "run-1")
	recA1.SessionID = sessA.ID
	recA1.PreviewCapabilityVerifier = "preview-verifier-a1"
	recA2 := shellTerminalRecord("shellterm-a2", "run-1")
	recA2.SessionID = sessA.ID
	recB := shellTerminalRecord("shellterm-b", "run-1")
	recB.SessionID = sessB.ID
	for _, rec := range []shelltermsvc.ShellTerminalRecord{recA1, recA2, recB} {
		if err := s.InsertShellTerminal(ctx, rec); err != nil {
			t.Fatalf("insert %s: %v", rec.HandleID, err)
		}
	}

	got, err := s.SelectShellTerminalsBySessionID(ctx, sessA.ID)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("terminals = %+v, want only session a's two shells", got)
	}
	for _, rec := range got {
		if rec.SessionID != sessA.ID {
			t.Errorf("terminal %s session id = %q, want %q", rec.HandleID, rec.SessionID, sessA.ID)
		}
		if rec.HandleID == recA1.HandleID && rec.PreviewCapabilityVerifier != recA1.PreviewCapabilityVerifier {
			t.Errorf("preview verifier = %q, want %q", rec.PreviewCapabilityVerifier, recA1.PreviewCapabilityVerifier)
		}
	}
}

func shellTerminalRecord(handleID, appRunID string) shelltermsvc.ShellTerminalRecord {
	return shelltermsvc.ShellTerminalRecord{
		HandleID:   handleID,
		WorkingDir: "/repos/portfolio",
		Title:      "portfolio",
		AppRunID:   appRunID,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}
}

func TestInsertAndSelectShellTerminalsByAppRunID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.InsertShellTerminal(ctx, shellTerminalRecord("shellterm-a", "run-1")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertShellTerminal(ctx, shellTerminalRecord("shellterm-b", "run-2")); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.SelectShellTerminalsByAppRunID(ctx, "run-1")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(got) != 1 || got[0].HandleID != "shellterm-a" {
		t.Fatalf("terminals = %+v, want only run-1's shell", got)
	}
	if got[0].ProjectID != "" {
		t.Errorf("project id = %q, want empty for a project-less shell", got[0].ProjectID)
	}
}

// A shell opened with no project stores NULL, not "" — an empty string would
// violate the projects foreign key.
func TestInsertShellTerminalWithoutProjectStoresNullProjectID(t *testing.T) {
	s := newTestStore(t)
	if err := s.InsertShellTerminal(context.Background(), shellTerminalRecord("shellterm-noproject", "run-1")); err != nil {
		t.Fatalf("insert without project: %v", err)
	}
}

func TestInsertShellTerminalWithProjectRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "portfolio")

	rec := shellTerminalRecord("shellterm-withproject", "run-1")
	rec.ProjectID = "portfolio"
	if err := s.InsertShellTerminal(ctx, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.SelectShellTerminalsByAppRunID(ctx, "run-1")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(got) != 1 || got[0].ProjectID != "portfolio" {
		t.Fatalf("terminals = %+v, want project portfolio", got)
	}
}

// TestSelectShellTerminalByHandleID: CloseShellTerminal uses this to learn a
// shell's session id before taking that session's teardown gate.
func TestSelectShellTerminalByHandleID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "portfolio")
	sess, err := s.CreateSession(ctx, sampleRecord("portfolio"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rec := shellTerminalRecord("shellterm-a", "run-1")
	rec.SessionID = sess.ID
	if err := s.InsertShellTerminal(ctx, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, found, err := s.SelectShellTerminalByHandleID(ctx, "shellterm-a")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true for an existing row")
	}
	if got.SessionID != sess.ID {
		t.Errorf("session id = %q, want %q", got.SessionID, sess.ID)
	}
}

func TestSelectShellTerminalByHandleIDReportsMissingRow(t *testing.T) {
	s := newTestStore(t)

	_, found, err := s.SelectShellTerminalByHandleID(context.Background(), "shellterm-missing")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if found {
		t.Error("found = true, want false for an unknown handle")
	}
}

func TestDeleteShellTerminalByHandleIDReportsWhetherRowExisted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.InsertShellTerminal(ctx, shellTerminalRecord("shellterm-a", "run-1")); err != nil {
		t.Fatalf("insert: %v", err)
	}

	deleted, err := s.DeleteShellTerminalByHandleID(ctx, "shellterm-a")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Error("deleted = false, want true for an existing row")
	}

	deleted, err = s.DeleteShellTerminalByHandleID(ctx, "shellterm-a")
	if err != nil {
		t.Fatalf("delete again: %v", err)
	}
	if deleted {
		t.Error("deleted = true on the second call, want false")
	}
}

func TestSelectAndDeleteShellTerminalsFromPreviousAppRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, rec := range []shelltermsvc.ShellTerminalRecord{
		shellTerminalRecord("shellterm-orphan1", "run-crashed"),
		shellTerminalRecord("shellterm-orphan2", "run-crashed"),
		shellTerminalRecord("shellterm-current", "run-current"),
	} {
		if err := s.InsertShellTerminal(ctx, rec); err != nil {
			t.Fatalf("insert %s: %v", rec.HandleID, err)
		}
	}

	orphans, err := s.SelectShellTerminalsFromPreviousAppRuns(ctx, "run-current")
	if err != nil {
		t.Fatalf("select orphans: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("orphans = %+v, want 2", orphans)
	}

	cleared, err := s.DeleteShellTerminalsFromPreviousAppRuns(ctx, "run-current")
	if err != nil {
		t.Fatalf("delete orphans: %v", err)
	}
	if cleared != 2 {
		t.Errorf("cleared = %d, want 2", cleared)
	}

	remaining, err := s.SelectShellTerminalsByAppRunID(ctx, "run-current")
	if err != nil {
		t.Fatalf("select current: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("remaining = %+v, want the current run's shell untouched", remaining)
	}
}

func TestRestorableShellTerminalsAcrossAppLaunches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		id, run   string
		transient bool
	}{
		{"shell-old", "old", false}, {"command-old", "old", true},
		{"shell-new", "new", false}, {"command-new", "new", true},
	} {
		rec := shellTerminalRecord(tc.id, tc.run)
		rec.Transient = tc.transient
		if err := s.InsertShellTerminal(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.SelectRestorableShellTerminals(ctx, "new")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("restorable rows = %+v", rows)
	}
	for _, row := range rows {
		if row.HandleID == "command-old" {
			t.Fatal("restored previous launch's command")
		}
		if row.HandleID == "command-new" && !row.Transient {
			t.Fatal("lost transient lifetime")
		}
	}
}

// endedStoreFixture inserts one session-scoped shell, as an ended tab would
// start out, against a real SQLite store.
func endedStoreFixture(t *testing.T) (*sqlite.Store, context.Context, shelltermsvc.ShellTerminalRecord) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "portfolio")
	sess, err := s.CreateSession(ctx, sampleRecord("portfolio"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rec := shellTerminalRecord("shellterm-old", "run-1")
	rec.SessionID = sess.ID
	rec.ProjectID = "portfolio"
	rec.Title = "Build"
	rec.TitleExplicit = true
	rec.CreatedAt = time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	if err := s.InsertShellTerminal(ctx, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return s, ctx, rec
}

func TestMarkShellTerminalEndedSetsEndedAtOnce(t *testing.T) {
	s, ctx, rec := endedStoreFixture(t)
	first := time.Date(2026, 11, 10, 20, 44, 0, 0, time.UTC)

	marked, err := s.MarkShellTerminalEnded(ctx, rec.HandleID, first)
	if err != nil || !marked {
		t.Fatalf("first mark = %v, %v; want true", marked, err)
	}
	marked, err = s.MarkShellTerminalEnded(ctx, rec.HandleID, first.Add(time.Hour))
	if err != nil || marked {
		t.Fatalf("second mark = %v, %v; want false", marked, err)
	}
	if marked, err := s.MarkShellTerminalEnded(ctx, "shellterm-ghost", first); err != nil || marked {
		t.Fatalf("unknown handle mark = %v, %v; want false", marked, err)
	}
	got, found, err := s.SelectShellTerminalByHandleID(ctx, rec.HandleID)
	if err != nil || !found {
		t.Fatalf("select = %v, %v", found, err)
	}
	if got.EndedAt == nil || !got.EndedAt.Equal(first) {
		t.Fatalf("ended at = %v, want the first mark %v", got.EndedAt, first)
	}
}

func TestUpdateShellTerminalAgentHarnessKeepsSameValue(t *testing.T) {
	s, ctx, rec := endedStoreFixture(t)
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessClaudeCode} {
		if err := s.UpdateShellTerminalAgentHarness(ctx, rec.HandleID, harness); err != nil {
			t.Fatalf("update %s: %v", harness, err)
		}
	}
	got, _, err := s.SelectShellTerminalByHandleID(ctx, rec.HandleID)
	if err != nil || got.LastAgentHarness != domain.HarnessClaudeCode {
		t.Fatalf("harness = %q, %v", got.LastAgentHarness, err)
	}
	if err := s.UpdateShellTerminalAgentHarness(ctx, rec.HandleID, domain.HarnessCodex); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.SelectShellTerminalByHandleID(ctx, rec.HandleID); got.LastAgentHarness != domain.HarnessCodex {
		t.Fatalf("harness = %q, want a changed harness stored", got.LastAgentHarness)
	}
}

func TestReplaceShellTerminalSwapsEndedRowInPlace(t *testing.T) {
	s, ctx, old := endedStoreFixture(t)
	if _, err := s.MarkShellTerminalEnded(ctx, old.HandleID, time.Date(2026, 11, 10, 20, 44, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	next := old
	next.HandleID = "shellterm-new"
	next.AppRunID = "run-2"
	next.EndedAt = nil
	next.LastAgentHarness = domain.HarnessClaudeCode

	replaced, err := s.ReplaceShellTerminal(ctx, old.HandleID, next)
	if err != nil || !replaced {
		t.Fatalf("replace = %v, %v; want true", replaced, err)
	}
	if _, found, _ := s.SelectShellTerminalByHandleID(ctx, old.HandleID); found {
		t.Fatal("old row still present after replace")
	}
	got, found, err := s.SelectShellTerminalByHandleID(ctx, next.HandleID)
	if err != nil || !found {
		t.Fatalf("select new = %v, %v", found, err)
	}
	if !got.CreatedAt.Equal(old.CreatedAt) || !got.TitleExplicit || got.Title != "Build" ||
		got.LastAgentHarness != domain.HarnessClaudeCode || got.EndedAt != nil || got.SessionID != old.SessionID {
		t.Fatalf("new row = %+v; want created_at, title, title_explicit and harness carried over, not ended", got)
	}
}

func TestReplaceShellTerminalMissingOldRowChangesNothing(t *testing.T) {
	s, ctx, existing := endedStoreFixture(t)
	next := existing
	next.HandleID = "shellterm-new"
	next.LastAgentHarness = domain.HarnessCodex

	replaced, err := s.ReplaceShellTerminal(ctx, "shellterm-ghost", next)
	if err != nil || replaced {
		t.Fatalf("replace = %v, %v; want false", replaced, err)
	}
	if _, found, _ := s.SelectShellTerminalByHandleID(ctx, next.HandleID); found {
		t.Fatal("replacement row inserted although the old row was missing")
	}
	rows, err := s.SelectShellTerminalsBySessionID(ctx, existing.SessionID)
	if err != nil || len(rows) != 1 || rows[0].HandleID != existing.HandleID {
		t.Fatalf("rows = %+v, %v; want only the untouched existing row", rows, err)
	}
}
