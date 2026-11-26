package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type crashDuringForkLoadDriver struct{ ports.ChatDriver }

func (crashDuringForkLoadDriver) ForkIntoHost(ctx context.Context, _, _ string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if err := cfg.RecordFork(ctx, "returned-child"); err != nil {
		return nil, err
	}
	os.Exit(17) // A daemon crash after session/fork returned, before session/load returned.
	return nil, nil
}

func TestReturnedForkSurvivesDaemonCrashForCleanup(t *testing.T) {
	if dir := os.Getenv("AO_TEST_FORK_CRASH_DIR"); dir != "" {
		svc := New(Options{DataDir: dir, AppRunID: "launch"})
		_, _ = svc.sides.store.ClaimSideLaunch(t.Context(), "launch", time.Now())
		side := domain.SideConversation{PolicyVersion: sidePolicyVersion, ID: "side", SessionID: "parent", MainConversationID: "main", AppRunID: "launch", ProviderHostID: "btw-launch-side", Harness: domain.HarnessClaudeCode, SourceProviderID: "parent-provider", Generation: "generation", ContextMode: "native", NativeAnchorID: "turn"}
		if _, _, err := svc.sides.store.CreateSideConversation(t.Context(), side); err != nil {
			t.Fatal(err)
		}
		svc.sides.open(side, StartConfig{}, crashDuringForkLoadDriver{})
		t.Fatal("did not crash during load")
	}
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestReturnedForkSurvivesDaemonCrashForCleanup$")
	cmd.Env = append(os.Environ(), "AO_TEST_FORK_CRASH_DIR="+dir)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 {
		t.Fatalf("crash process: %v\n%s", err, output)
	}

	stopped := false
	svc := New(Options{DataDir: dir, AppRunID: "launch", StopProviderHost: func(_ context.Context, host domain.SessionID) error {
		if host != "btw-launch-side" {
			t.Errorf("stopped wrong host: %s", host)
		}
		stopped = true
		return nil
	}})
	parent := &sideCleanupTestConversation{deleteErr: errors.New("provider temporarily unavailable"), onDelete: func() {
		if !stopped {
			t.Error("deleted child before stopping its host")
		}
	}}
	svc.controllers["parent"] = &Controller{conv: parent, harness: domain.HarnessClaudeCode}
	_, _ = svc.sides.store.ClaimSideLaunch(t.Context(), "launch", time.Now())
	if err := svc.sides.recoverForkRecords(t.Context(), "launch"); err != nil {
		t.Fatal(err)
	}
	svc.sides.retryCleanup()
	if len(parent.deleted) != 1 || parent.deleted[0] != "returned-child" {
		t.Fatalf("recovered deletion: %v", parent.deleted)
	}
	files, err := filepath.Glob(filepath.Join(dir, "side-forks", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("failed cleanup lost record: %v %v", files, err)
	}
	// A second crash may recover the pending cleanup through Electron's JSON snapshot.
	body, err := json.Marshal(svc.sides.store.(*memorySideStore).export("launch"))
	if err != nil {
		t.Fatal(err)
	}
	var records []SideRecoveryRecord
	if err := json.Unmarshal(body, &records); err != nil {
		t.Fatal(err)
	}
	recovered := newMemorySideStore()
	_, _ = recovered.ClaimSideLaunch(t.Context(), "launch", time.Now())
	if _, err := recovered.recover("launch", records, time.Now()); err != nil {
		t.Fatal(err)
	}
	items, err := recovered.SideProviderCleanupPending(t.Context())
	if err != nil || len(items) != 1 || items[0].Harness != domain.HarnessClaudeCode || items[0].ForkID != "returned-child" {
		t.Fatalf("second daemon cleanup: %v %v", items, err)
	}
	parent.deleteErr = nil
	svc.sides.retryCleanup()
	files, err = filepath.Glob(filepath.Join(dir, "side-forks", "*.json"))
	if err != nil || len(files) != 0 || len(parent.deleted) != 2 {
		t.Fatalf("cleanup did not settle: files=%v deleted=%v error=%v", files, parent.deleted, err)
	}
	store := svc.sides.store.(*memorySideStore)
	if !store.closed["side"] {
		t.Fatal("Electron recovery could resurrect the interrupted side")
	}
}

func TestForkJournalPreservesAdoptedSidesFromCurrentLaunch(t *testing.T) {
	svc := New(Options{DataDir: t.TempDir(), AppRunID: "launch"})
	side := domain.SideConversation{MainConversationID: "main", ID: "side", AppRunID: "launch", SessionID: "parent", ProviderHostID: "btw-launch-side", Harness: domain.HarnessClaudeCode}
	if err := svc.sides.writeForkRecord(t.Context(), side, "child", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.sides.recoverForkRecords(t.Context(), "launch"); err != nil {
		t.Fatal(err)
	}
	if len(svc.sides.store.SideHostCleanupPending(t.Context())) != 0 {
		t.Fatal("live side scheduled for deletion")
	}
	if err := svc.sides.recoverForkRecords(t.Context(), "next-launch"); err != nil {
		t.Fatal(err)
	}
	if len(svc.sides.store.SideHostCleanupPending(t.Context())) != 1 {
		t.Fatal("previous launch child was not retired")
	}
}

func TestClaimSideLaunchDoesNotRetireItsOwnInProgressFork(t *testing.T) {
	svc := New(Options{DataDir: t.TempDir(), AppRunID: "launch"})
	defer svc.sides.cancel()
	if err := svc.ClaimSideChatLaunch(t.Context(), "launch"); err != nil {
		t.Fatal(err)
	}
	side := domain.SideConversation{ID: "side", AppRunID: "launch", MainConversationID: "main", SessionID: "parent", ProviderHostID: "btw-launch-side"}
	if err := svc.sides.writeForkRecord(t.Context(), side, "child", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClaimSideChatLaunch(t.Context(), "launch"); err != nil {
		t.Fatal(err)
	}
	if len(svc.sides.store.SideHostCleanupPending(t.Context())) != 0 {
		t.Fatal("repeated claim retired a live fork")
	}
}

func TestFailedForkLoadWaitsForHostCleanupBeforeDeleting(t *testing.T) {
	store := newMemorySideStore()
	ctx := t.Context()
	_, _ = store.ClaimSideLaunch(ctx, "launch", time.Now())
	side := domain.SideConversation{ID: "side", AppRunID: "launch", SessionID: "parent", ProviderHostID: "btw-launch-side", Generation: "g"}
	_, _, _ = store.CreateSideConversation(ctx, side)
	if err := store.RegisterSideFork(ctx, side.ID, side.Generation, "child", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSideFailed(ctx, side.ID, side.Generation, ErrSidePolicyRecreate.Error(), time.Now()); err != nil {
		t.Fatal(err)
	}
	items, err := store.SideProviderCleanupPending(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("deletion before host stop: %v %v", items, err)
	}
	store.CompleteSideHostCleanup(ctx, side.ID)
	items, err = store.SideProviderCleanupPending(ctx)
	if err != nil || len(items) != 1 || items[0].ForkID != "child" {
		t.Fatalf("cleanup after host stop: %v %v", items, err)
	}
}

func TestFailedForkCleanupDoesNotExportDuplicateOrDeletedChild(t *testing.T) {
	store := newMemorySideStore()
	ctx := t.Context()
	_, _ = store.ClaimSideLaunch(ctx, "launch", time.Now())
	side := domain.SideConversation{PolicyVersion: sidePolicyVersion, ID: "side", AppRunID: "launch", MainConversationID: "main", SessionID: "parent", ProviderHostID: "btw-launch-side", Generation: "g"}
	_, _, _ = store.CreateSideConversation(ctx, side)
	_ = store.RegisterSideFork(ctx, side.ID, side.Generation, "child", time.Now())
	_ = store.SetSideFailed(ctx, side.ID, side.Generation, ErrSidePolicyRecreate.Error(), time.Now())
	records := store.export("launch")
	if len(records) != 1 {
		t.Fatalf("duplicate recovery records: %v", records)
	}
	recovered := newMemorySideStore()
	_, _ = recovered.ClaimSideLaunch(ctx, "launch", time.Now())
	created, err := recovered.recover("launch", records, time.Now())
	if err != nil || len(created) != 1 || !created[0].RecreateRequired || created[0].State != "failed" {
		t.Fatalf("unsafe recovery: %v %v", created, err)
	}
	store.CompleteSideHostCleanup(ctx, side.ID)
	_ = store.CompleteSideProviderCleanup(ctx, "child")
	records = store.export("launch")
	if len(records) != 1 || records[0].ProviderForkID != "" {
		t.Fatalf("deleted child exported: %v", records)
	}
}
