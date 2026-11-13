package storagemgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func waitIdle(t *testing.T, svc *Service) Report {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		report, err := svc.Report(context.Background())
		if err != nil {
			t.Fatalf("report: %v", err)
		}
		if !report.Scanning && !report.Cleaning {
			return report
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("storage service did not settle")
	return Report{}
}

func TestCleanBeforeFirstScanAsksToRetry(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore(), nil, nil, nil)
	if _, err := svc.Clean(context.Background(), CleanRequest{DryRun: true}); !errors.Is(err, ErrScanRequired) {
		t.Fatalf("err = %v, want ErrScanRequired", err)
	}
	waitIdle(t, svc)
}

func TestCleanRejectsUnknownKind(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore(), nil, nil, nil)
	if _, err := svc.Clean(context.Background(), CleanRequest{Kinds: []domain.StorageCleanKind{"everything"}}); err == nil {
		t.Fatal("unknown kind must be rejected")
	}
}

func TestBackupCleanupKeepsNewestAndAudits(t *testing.T) {
	store := newFakeStore()
	svc, layout := newTestService(t, store, nil, nil, nil)
	old := filepath.Join(layout.DataDir, "ao.db.backup-before-0170-repair")
	newest := filepath.Join(layout.DataDir, "ao.db.bak-before-0200")
	writeFile(t, old, "old backup")
	writeFile(t, newest, "newest backup")
	past := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc.StartScan()
	report := waitIdle(t, svc)
	if report.Categories[2].Category != domain.StorageDBBackups || report.Categories[2].Bytes == 0 {
		t.Fatalf("backup category = %+v", report.Categories[2])
	}

	preview, err := svc.Clean(ctx, CleanRequest{DryRun: true, Kinds: []domain.StorageCleanKind{domain.StorageCleanBackups}})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(preview.Actions) != 1 || preview.Actions[0].Target != old || preview.Actions[0].Result != domain.StorageActionPlanned {
		t.Fatalf("preview = %+v, want only the old backup", preview.Actions)
	}
	if !pathExists(old) {
		t.Fatal("a dry run must not delete")
	}

	if _, err := svc.Clean(ctx, CleanRequest{Kinds: []domain.StorageCleanKind{domain.StorageCleanBackups}}); err != nil {
		t.Fatalf("clean: %v", err)
	}
	report = waitIdle(t, svc)
	if pathExists(old) || !pathExists(newest) {
		t.Fatalf("old exists=%v newest exists=%v, want old removed and newest kept", pathExists(old), pathExists(newest))
	}
	if report.LastClean == nil || report.LastClean.FreedBytes == 0 {
		t.Fatalf("last clean = %+v", report.LastClean)
	}
	actions := store.snapshotActions()
	if len(actions) == 0 || actions[0].Kind != domain.StorageCleanBackups || actions[0].Result != domain.StorageActionDone {
		t.Fatalf("audit = %+v", actions)
	}
}

func TestOffloadSkippedWhenAgentMayRun(t *testing.T) {
	store := newFakeStore()
	svc, _ := newTestService(t, store, nil, nil, refuseAll{})
	action := svc.runOffload(context.Background(), PlannedAction{Kind: domain.StorageCleanOffload, SessionID: "p-1", Target: "/w/p-1", Bytes: 10})
	if action.Result != domain.StorageActionSkipped || action.Bytes != 0 {
		t.Fatalf("action = %+v, want skipped with nothing freed", action)
	}
}

type refuseAll struct{}

func (refuseAll) WithQuietSession(context.Context, domain.SessionID, func(domain.SessionRecord) error) error {
	return sessionmanager.ErrSessionNotQuiet
}

func TestUpdateSettingsValidates(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore(), nil, nil, nil)
	settings := domain.DefaultStorageSettings()
	settings.OffloadIdleHours = 0
	if _, err := svc.UpdateSettings(context.Background(), settings); err == nil {
		t.Fatal("offloadIdleHours=0 must be rejected")
	}
	settings.OffloadIdleHours = 72
	got, err := svc.UpdateSettings(context.Background(), settings)
	if err != nil || got.OffloadIdleHours != 72 || got.UpdatedAt.IsZero() {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestAutomaticPassWithAutoCleanOffOnlyLogsDryRun(t *testing.T) {
	store := newFakeStore()
	store.settings.AutoClean = false
	svc, layout := newTestService(t, store, nil, nil, nil)
	logFile := filepath.Join(layout.Logs(), "old.jsonl")
	writeFile(t, logFile, "log")
	past := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(logFile, past, past); err != nil {
		t.Fatal(err)
	}
	// With auto-clean off nothing is automatic, so the pass has nothing to run.
	svc.automaticPass(context.Background())
	if !pathExists(logFile) {
		t.Fatal("auto-clean off must not delete")
	}

	store.settings.AutoClean = true
	svc.autoAllowed = false
	svc.automaticPass(context.Background())
	if !pathExists(logFile) {
		t.Fatal("the kill switch must not delete")
	}
	found := false
	for _, action := range store.snapshotActions() {
		if action.Target == logFile && action.DryRun && action.Result == domain.StorageActionPlanned {
			found = true
		}
	}
	if !found {
		t.Fatalf("kill switch should log the would-be action: %+v", store.snapshotActions())
	}
}
