package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type closeOwnershipParentStore struct{ Store }

func (closeOwnershipParentStore) ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error) {
	return domain.ConversationRecord{ID: "main"}, nil
}

func closeOwnershipFixture(t *testing.T, dir string, inMemory bool) (*Service, domain.SideConversation, *sideCleanupTestConversation) {
	t.Helper()
	svc := New(Options{DataDir: dir, AppRunID: "launch", Store: closeOwnershipParentStore{}, Sessions: sideSessionReader{}})
	_, _ = svc.sides.store.ClaimSideLaunch(t.Context(), "launch", time.Now())
	side := domain.SideConversation{PolicyVersion: sidePolicyVersion, ID: "side", SessionID: "parent", MainConversationID: "main", AppRunID: "launch", ProviderHostID: "btw-launch-side", Harness: domain.HarnessCodex, Generation: "generation", State: "opening"}
	if inMemory {
		if _, _, err := svc.sides.store.CreateSideConversation(t.Context(), side); err != nil {
			t.Fatal(err)
		}
		if err := svc.sides.store.RegisterSideFork(t.Context(), side.ID, side.Generation, "child", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	side.ProviderForkID = "child"
	if err := svc.sides.writeForkRecord(t.Context(), side, "child", true); err != nil {
		t.Fatal(err)
	}
	conv := &sideCleanupTestConversation{}
	svc.controllers["parent"] = &Controller{conv: conv, harness: domain.HarnessCodex}
	return svc, side, conv
}

func TestSideCloseRetiresOwnershipBeforeEffects(t *testing.T) {
	for _, inMemory := range []bool{true, false} {
		t.Run(map[bool]string{true: "normal", false: "journal-only"}[inMemory], func(t *testing.T) {
			svc, side, conv := closeOwnershipFixture(t, t.TempDir(), inMemory)
			stopped := false
			svc.stopProviderHost = func(context.Context, domain.SessionID) error {
				record, err := svc.sides.readForkRecordLocked(side.ID)
				if err != nil || record.Adopted {
					t.Fatalf("host stopped before durable retirement: %#v %v", record, err)
				}
				if _, err := svc.sides.store.SideConversation(t.Context(), side.ID); !errors.Is(err, ErrSideClosed) {
					t.Fatalf("host stopped before logical closure: %v", err)
				}
				stopped = true
				return nil
			}
			conv.onDelete = func() {
				if !stopped {
					t.Error("native child deleted before host shutdown")
				}
			}
			if err := svc.CloseIndependentSideChat(t.Context(), "parent", side.ID); err != nil {
				t.Fatal(err)
			}
			if !stopped || len(conv.deleted) != 1 {
				t.Fatalf("cleanup effects: stopped=%v deleted=%v", stopped, conv.deleted)
			}
			if _, err := os.Stat(svc.sides.forkJournalPath(side.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("journal remains after successful cleanup: %v", err)
			}
			svc.sides.deleteRegisteredFork(t.Context(), side, "child")
			if len(conv.deleted) != 1 {
				t.Fatal("queued duplicate cleanup repeated a completed native deletion")
			}
			if err := svc.sides.registerFork(t.Context(), side, "child"); !errors.Is(err, ErrSideClosed) {
				t.Fatalf("late same-child callback: %v", err)
			}
			if len(svc.sides.store.(*memorySideStore).export("launch")) != 0 {
				t.Fatal("late callback resurrected drained cleanup")
			}
		})
	}
}

func TestSideCloseCannotCommitWhenJournalCannotBeWritten(t *testing.T) {
	svc, side, conv := closeOwnershipFixture(t, t.TempDir(), true)
	// Use a regular file as the data directory: the failure occurs while
	// creating the durable cleanup path, before any logical/provider effect.
	blocked := t.TempDir() + "/file"
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	svc.dataDir = blocked
	svc.stopProviderHost = func(context.Context, domain.SessionID) error { t.Error("stopped without durable intent"); return nil }
	if err := svc.CloseIndependentSideChat(t.Context(), "parent", side.ID); err == nil {
		t.Fatal("accepted unwritable close journal")
	}
	if _, err := svc.sides.store.SideConversation(t.Context(), side.ID); err != nil || len(conv.deleted) != 0 {
		t.Fatalf("write failure changed chat: %v deleted=%v", err, conv.deleted)
	}
}

func TestSideCloseJournalWriteFailureHasNoEffects(t *testing.T) {
	for _, inMemory := range []bool{true, false} {
		t.Run(map[bool]string{true: "normal", false: "journal-only"}[inMemory], func(t *testing.T) {
			svc, side, conv := closeOwnershipFixture(t, t.TempDir(), inMemory)
			if err := os.WriteFile(svc.sides.forkJournalPath(side.ID), []byte("corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
			stopped := false
			svc.stopProviderHost = func(context.Context, domain.SessionID) error { stopped = true; return nil }
			if err := svc.CloseIndependentSideChat(t.Context(), "parent", side.ID); err == nil {
				t.Fatal("accepted unrecordable close intent")
			}
			if stopped || len(conv.deleted) != 0 {
				t.Fatal("failed journal validation caused provider effects")
			}
			if inMemory {
				if _, err := svc.sides.store.SideConversation(t.Context(), side.ID); err != nil {
					t.Fatalf("failed journal write logically closed chat: %v", err)
				}
			}
		})
	}
}

func TestSideCloseJournalRequiresExactOwnership(t *testing.T) {
	for _, field := range []string{"run", "session", "parent", "host", "side", "harness", "fork"} {
		t.Run(field, func(t *testing.T) {
			svc, side, _ := closeOwnershipFixture(t, t.TempDir(), false)
			record, err := svc.sides.readForkRecordLocked(side.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "run":
				record.RunID, record.HostID = "other", "btw-other-side"
			case "session":
				record.SessionID = "other"
			case "parent":
				record.MainConversationID = "other"
			case "host":
				record.HostID = "other"
			case "side":
				record.SideID, record.HostID = "other", "btw-launch-other"
			case "harness":
				record.Harness = "unknown"
			case "fork":
				record.ForkID = ""
			}
			body, _ := json.Marshal(record)
			if err := os.WriteFile(svc.sides.forkJournalPath(side.ID), body, 0o600); err != nil {
				t.Fatal(err)
			}
			svc.stopProviderHost = func(context.Context, domain.SessionID) error { t.Error("stopped unowned host"); return nil }
			if err := svc.CloseIndependentSideChat(t.Context(), "parent", side.ID); !errors.Is(err, ErrSideUnavailable) {
				t.Fatalf("ownership mismatch accepted: %v", err)
			}
		})
	}
}

func TestSideCloseRetirementSurvivesCrash(t *testing.T) {
	if dir := os.Getenv("AO_TEST_CLOSE_CRASH_DIR"); dir != "" {
		svc, side, _ := closeOwnershipFixture(t, dir, true)
		svc.stopProviderHost = func(context.Context, domain.SessionID) error { os.Exit(19); return nil }
		_ = svc.CloseIndependentSideChat(t.Context(), "parent", side.ID)
		t.Fatal("close did not reach host stop")
	}
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestSideCloseRetirementSurvivesCrash$")
	cmd.Env = append(os.Environ(), "AO_TEST_CLOSE_CRASH_DIR="+dir)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 19 {
		t.Fatalf("crash process: %v\n%s", err, output)
	}
	svc := New(Options{DataDir: dir, AppRunID: "launch"})
	_, _ = svc.sides.store.ClaimSideLaunch(t.Context(), "launch", time.Now())
	if err := svc.sides.recoverForkRecords(t.Context(), "launch"); err != nil {
		t.Fatal(err)
	}
	records := svc.sides.store.(*memorySideStore).export("launch")
	if len(records) != 1 || !records[0].CleanupOnly || records[0].HostStopped {
		t.Fatalf("crashed close lost cleanup ownership: %#v", records)
	}
}

func TestSideCloseCancellationRetainsAcceptedIntent(t *testing.T) {
	svc, side, conv := closeOwnershipFixture(t, t.TempDir(), true)
	ctx, cancel := context.WithCancel(t.Context())
	svc.stopProviderHost = func(context.Context, domain.SessionID) error { cancel(); return ctx.Err() }
	if err := svc.CloseIndependentSideChat(ctx, "parent", side.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("close result: %v", err)
	}
	record, err := svc.sides.readForkRecordLocked(side.ID)
	if err != nil || record.Adopted || len(conv.deleted) != 0 {
		t.Fatalf("cancelled stop lost retirement: %#v %v deleted=%v", record, err, conv.deleted)
	}
	if _, err := svc.sides.store.SideConversation(t.Context(), side.ID); !errors.Is(err, ErrSideClosed) {
		t.Fatalf("cancelled request restored conversation: %v", err)
	}
	svc.stopProviderHost = func(context.Context, domain.SessionID) error { return nil }
	svc.sides.retryCleanup()
	if len(conv.deleted) != 1 {
		t.Fatalf("existing cleanup owner did not finish: %v", conv.deleted)
	}
}

type staleSideOpenDriver struct {
	ports.ChatDriver
	starts atomic.Int32
}

func (d *staleSideOpenDriver) Start(context.Context, ports.ChatStartConfig) (ports.ChatConversation, error) {
	d.starts.Add(1)
	return nil, errors.New("unexpected fresh fork")
}

func TestSideCloseStaleOpeningCannotReplaceKnownFork(t *testing.T) {
	svc, side, _ := closeOwnershipFixture(t, t.TempDir(), true)
	side.ProviderForkID = "" // Snapshot queued before the first open registered its child.
	driver := &staleSideOpenDriver{}
	svc.sides.open(side, StartConfig{}, driver)
	if driver.starts.Load() != 0 {
		t.Fatal("stale opening started another provider conversation")
	}
	record, err := svc.sides.readForkRecordLocked(side.ID)
	if err != nil || record.ForkID != "child" || !record.Adopted {
		t.Fatalf("stale opening replaced ownership: %#v %v", record, err)
	}
}

func TestSideCloseLateForkRequiresFreshHostStop(t *testing.T) {
	svc := New(Options{DataDir: t.TempDir(), AppRunID: "launch"})
	_, _ = svc.sides.store.ClaimSideLaunch(t.Context(), "launch", time.Now())
	side := domain.SideConversation{PolicyVersion: sidePolicyVersion, ID: "side", SessionID: "parent", MainConversationID: "main", AppRunID: "launch", ProviderHostID: "btw-launch-side", Harness: domain.HarnessCodex, Generation: "generation"}
	_, _, _ = svc.sides.store.CreateSideConversation(t.Context(), side)
	_, _ = svc.sides.store.CloseSideConversation(t.Context(), side.ID, time.Now())
	conv := &sideCleanupTestConversation{}
	svc.controllers["parent"] = &Controller{conv: conv, harness: domain.HarnessCodex}
	entered, release := make(chan struct{}), make(chan struct{})
	var stops atomic.Int32
	svc.stopProviderHost = func(context.Context, domain.SessionID) error {
		if stops.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	}
	result := make(chan error, 1)
	go func() { result <- svc.sides.stopSideHost(t.Context(), side) }()
	<-entered
	// A native child becomes known while the previous, fork-less stop is in flight.
	if err := svc.sides.registerFork(t.Context(), side, "child"); !errors.Is(err, ErrSideClosed) {
		t.Fatalf("late fork result: %v", err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrSideNotReady) {
		t.Fatalf("old stop confirmed newly reported ownership: %v", err)
	}
	records := svc.sides.store.(*memorySideStore).export("launch")
	if len(records) != 1 || records[0].HostStopped || records[0].ProviderForkID != "child" {
		t.Fatalf("late ownership was lost or falsely stopped: %#v", records)
	}
	svc.sides.retryCleanup()
	if stops.Load() != 2 || len(conv.deleted) != 1 {
		t.Fatalf("new ownership cleanup: stops=%d deleted=%v", stops.Load(), conv.deleted)
	}
}

func TestSideCloseRestorePreservesShutdownProof(t *testing.T) {
	svc, side, conv := closeOwnershipFixture(t, t.TempDir(), true)
	svc.stopProviderHost = func(context.Context, domain.SessionID) error { return errors.New("stop failed") }
	if err := svc.CloseIndependentSideChat(t.Context(), "parent", side.ID); err == nil {
		t.Fatal("expected shutdown failure")
	}
	store := svc.sides.store.(*memorySideStore)
	stale := store.export("launch")
	store.CompleteSideHostCleanup(t.Context(), side.ID)
	if _, err := store.recover("launch", stale, time.Now()); err != nil {
		t.Fatal(err)
	}
	if records := store.export("launch"); len(records) != 1 || !records[0].HostStopped {
		t.Fatalf("stale snapshot downgraded stopped proof: %#v", records)
	}
	stale[0].ProviderForkID = ""
	if _, err := store.recover("launch", stale, time.Now()); err != nil {
		t.Fatal(err)
	}
	if records := store.export("launch"); len(records) != 1 || records[0].ProviderForkID != "child" {
		t.Fatalf("stale snapshot erased known child: %#v", records)
	}
	stale[0].ProviderForkID = "other-child"
	if _, err := store.recover("launch", stale, time.Now()); !errors.Is(err, ErrSideUnavailable) {
		t.Fatalf("restore replaced pending native child: %v", err)
	}
	stale[0].ProviderForkID = "child"
	svc.sides.retryCleanup()
	if len(conv.deleted) != 1 {
		t.Fatalf("stopped host cleanup failed: %v", conv.deleted)
	}
	if _, err := store.recover("launch", stale, time.Now()); err != nil {
		t.Fatal(err)
	}
	if records := store.export("launch"); len(records) != 0 {
		t.Fatalf("old restore resurrected completed cleanup: %#v", records)
	}
}

func TestSideCloseJournalRemovalFailureKeepsOnlyNativeCleanup(t *testing.T) {
	svc, side, conv := closeOwnershipFixture(t, t.TempDir(), true)
	var stops int
	svc.stopProviderHost = func(context.Context, domain.SessionID) error { stops++; return nil }
	conv.onDelete = func() {
		if err := os.WriteFile(svc.sides.forkJournalPath(side.ID), []byte("unreadable journal"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.CloseIndependentSideChat(t.Context(), "parent", side.ID); err != nil {
		t.Fatalf("native bookkeeping failure reversed confirmed shutdown: %v", err)
	}
	records := svc.sides.store.(*memorySideStore).export("launch")
	if len(records) != 1 || !records[0].CleanupOnly || !records[0].HostStopped {
		t.Fatalf("native-only cleanup lost ownership: %#v", records)
	}
	conv.onDelete = nil
	body, _ := json.Marshal(sideForkRecord{MainConversationID: "main", SideID: side.ID, RunID: "launch", SessionID: "parent", HostID: side.ProviderHostID, ForkID: "child", Harness: domain.HarnessCodex})
	if err := os.WriteFile(svc.sides.forkJournalPath(side.ID), body, 0o600); err != nil {
		t.Fatal(err)
	}
	svc.sides.retryCleanup()
	if stops != 1 || len(conv.deleted) != 2 || len(svc.sides.store.(*memorySideStore).export("launch")) != 0 {
		t.Fatalf("native-only retry: stops=%d deleted=%v", stops, conv.deleted)
	}
}
