package sessionmanager

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// moveStore records moves like the SQLite store.
type moveStore struct {
	*fakeStore
	moves map[domain.SessionID]domain.SessionMovedTo
}

func (s *moveStore) RecordPendingTransfer(_ context.Context, id domain.SessionID, dest domain.SessionMovedTo, _ time.Time) error {
	if at, ok := s.moves[id]; !ok || at.Pending() {
		s.moves[id] = dest
	}
	return nil
}

func (s *moveStore) CancelPendingTransfer(_ context.Context, id domain.SessionID, transferID string) (bool, error) {
	at, ok := s.moves[id]
	if !ok || !at.Pending() || at.TransferID != transferID {
		return false, nil
	}
	delete(s.moves, id)
	return true, nil
}

func (s *moveStore) FinishPendingTransfer(_ context.Context, id domain.SessionID, moved domain.SessionMovedTo, _ time.Time) (bool, error) {
	at, ok := s.moves[id]
	if at == moved {
		return true, nil
	}
	if !ok || !at.Pending() || at.TransferID != moved.TransferID {
		return false, nil
	}
	s.moves[id] = moved
	return true, nil
}

func (s *moveStore) SessionMovedTo(_ context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error) {
	at, ok := s.moves[id]
	return at, ok, nil
}

type moveWorkspace struct {
	*fakeWorkspace
	bundle func(path string) error
}

func (w *moveWorkspace) CheckTransfer(context.Context, ports.WorkspaceInfo) error { return nil }
func (w *moveWorkspace) BundleTransfer(_ context.Context, _ ports.WorkspaceInfo, _, _, bundlePath string) (string, bool, bool, error) {
	return strings.Repeat("a", 40), true, true, w.bundle(bundlePath)
}
func (w *moveWorkspace) FetchTransfer(context.Context, string, string, string, string, string) error {
	return nil
}
func (w *moveWorkspace) AlignTransferBranch(context.Context, string, string, string) error {
	return nil
}
func (w *moveWorkspace) RevParse(context.Context, string, string) (string, error)         { return "", nil }
func (w *moveWorkspace) ReleaseTransferBranch(context.Context, ports.WorkspaceInfo) error { return nil }
func (w *moveWorkspace) RestoreTransferIndex(context.Context, ports.WorkspaceInfo, string) error {
	return nil
}

func newMoveManager(t *testing.T) (*Manager, *moveStore, *moveWorkspace, *fakeRuntime) {
	t.Helper()
	m, st, rt, ws := newManager()
	store := &moveStore{fakeStore: st, moves: map[domain.SessionID]domain.SessionMovedTo{}}
	workspace := &moveWorkspace{fakeWorkspace: ws, bundle: writeBundle}
	m.store, m.workspace, m.dataDir = store, workspace, t.TempDir()
	m.agents = singleAgent{agent: transcriptAgent{dir: t.TempDir()}}
	st.sessions["mer-1"] = pastStartupGate(domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1", RuntimeHandleID: "tmux-mer-1", RuntimeLaunchID: "launch-1"},
	})
	rt.aliveByHandle = map[string]bool{"tmux-mer-1": true}
	return m, store, workspace, rt
}

func writeBundle(path string) error { return os.WriteFile(path, []byte("bundle"), 0o600) }

// transferLeft reports whether a move left its folder behind.
func transferLeft(t *testing.T, m *Manager, transferID string) bool {
	t.Helper()
	dir, err := m.transferDir(transferID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(dir)
	return err == nil
}

var toB = domain.SessionTransferDestination{HostID: "h_b", Label: "MacBook B", ProjectID: "web"}

func TestPrepareTransferReservesTheSessionBeforeItStops(t *testing.T) {
	m, store, ws, _ := newMoveManager(t)
	resumed, switched := errors.New("not tried"), errors.New("not tried")
	ws.bundle = func(path string) error {
		_, resumed = m.ResumeAgentWithMode(ctx, "mer-1")
		rec := store.sessions["mer-1"]
		_, switched = m.relaunchSession(ctx, "switch", rec, domain.ProjectRecord{ID: "mer"}, workspaceInfo(rec), nil)
		return writeBundle(path)
	}
	transfer, err := m.PrepareTransfer(ctx, "mer-1", "trfirst00", toB, false)
	if err != nil || transfer.PreservedRef != "refs/ao/preserved/trfirst00" || !transfer.Bundle || transfer.Archive == nil {
		t.Fatalf("prepare = %+v, %v", transfer, err)
	}
	if !errors.Is(resumed, ErrTransferPending) || !errors.Is(switched, ErrTransferPending) {
		t.Fatalf("during prepare: resume = %v, switch = %v; want ErrTransferPending", resumed, switched)
	}
	if want := (domain.SessionMovedTo{TransferID: "trfirst00", HostID: "h_b", Label: "MacBook B", ProjectID: "web"}); store.moves["mer-1"] != want {
		t.Fatalf("pending = %+v, want %+v", store.moves["mer-1"], want)
	}
	if again, err := m.PrepareTransfer(ctx, "mer-1", "trsecond0", toB, false); err != nil || again.TransferID != "trfirst00" {
		t.Fatalf("second prepare = %q, %v; want the unfinished move", again.TransferID, err)
	}
	before := store.sessions["mer-1"]
	if err := m.SwitchTUIProvider(ctx, "mer-1", domain.ProviderSwitchRequest{TargetHarness: domain.HarnessCodex}); !errors.Is(err, ErrTransferPending) {
		t.Fatalf("switch agent = %v, want ErrTransferPending", err)
	}
	if !reflect.DeepEqual(store.sessions["mer-1"], before) {
		t.Fatalf("a refused switch changed the session: %+v", store.sessions["mer-1"])
	}
	other := toB
	other.ProjectID = "web-2"
	if _, err := m.PrepareTransfer(ctx, "mer-1", "trthird00", other, false); !errors.Is(err, ErrTransferPending) {
		t.Fatalf("prepare to another project = %v, want ErrTransferPending", err)
	}
}

func TestPrepareTransferFailureReleasesTheSession(t *testing.T) {
	m, store, ws, _ := newMoveManager(t)
	ws.bundle = func(path string) error {
		if err := writeBundle(path); err != nil {
			return err
		}
		return ports.ErrTransferLFS
	}
	if _, err := m.PrepareTransfer(ctx, "mer-1", "trfirst00", toB, false); !errors.Is(err, ports.ErrTransferLFS) {
		t.Fatalf("prepare = %v", err)
	}
	if len(store.moves) != 0 || transferLeft(t, m, "trfirst00") {
		t.Fatalf("after a refused bundle: pending %v; want none and no files", store.moves)
	}
}

func TestPrepareTransferWithoutAConversationCarriesTheWork(t *testing.T) {
	m, _, _, _ := newMoveManager(t)
	m.agents = fakeAgents{}
	transfer, err := m.PrepareTransfer(ctx, "mer-1", "trfirst00", toB, false)
	if err != nil || transfer.Native != nil || !transfer.Bundle || transfer.Archive == nil {
		t.Fatalf("prepare = %+v, %v; want the bundle alone", transfer, err)
	}
}

func TestCommitAfterKeepItHereLeavesTheSessionRunning(t *testing.T) {
	m, store, _, rt := newMoveManager(t)
	if _, err := m.PrepareTransfer(ctx, "mer-1", "trfirst00", toB, false); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelTransfer(ctx, "mer-1", "trfirst00"); err != nil {
		t.Fatal(err)
	}
	if transferLeft(t, m, "trfirst00") {
		t.Fatal("cancel left the transfer's files")
	}
	stopped := rt.destroyed
	done := toB
	done.SessionID = "web-9"
	if _, err := m.CommitTransfer(ctx, "mer-1", "trfirst00", done); !errors.Is(err, ErrTransferNotPending) {
		t.Fatalf("late commit = %v, want ErrTransferNotPending", err)
	}
	if store.sessions["mer-1"].IsTerminated || rt.destroyed != stopped || len(store.moves) != 0 {
		t.Fatalf("a late commit ended the session: %+v, moves %v", store.sessions["mer-1"], store.moves)
	}

	if _, err := m.PrepareTransfer(ctx, "mer-1", "trsecond0", toB, false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if moved, err := m.CommitTransfer(ctx, "mer-1", "trsecond0", done); err != nil || moved.SessionID != "web-9" || moved.ProjectID != "web" {
			t.Fatalf("commit = %+v, %v", moved, err)
		}
	}
	if !store.sessions["mer-1"].IsTerminated {
		t.Fatal("commit left the session running")
	}
}

func TestReceivedSessionAcknowledgesOnlyAFinishedReceive(t *testing.T) {
	m, store, _, rt := newMoveManager(t)
	transfer := domain.SessionTransfer{TransferID: "trfirst00", Branch: "feat/x", HeadSHA: strings.Repeat("a", 40)}
	add := func(id domain.SessionID, requestID string, committed bool) {
		rec := store.sessions["mer-1"]
		rec.ID, rec.ClientRequestID, rec.ClientRequestHash, rec.ClientRequestCommitted = id, requestID, transferRequestHash(transfer), committed
		rec.Metadata.RuntimeHandleID = "tmux-" + string(id)
		store.sessions[id] = rec
	}

	if rec, requestID, err := m.receivedSession(ctx, transfer); err != nil || rec.ID != "" || requestID != "transfer-trfirst00" {
		t.Fatalf("first receive = %q %q %v", rec.ID, requestID, err)
	}

	// The daemon stopped after the session started and before it was acknowledged.
	add("mer-2", "transfer-trfirst00", false)
	rec, requestID, err := m.receivedSession(ctx, transfer)
	if err != nil || rec.ID != "" || requestID != "transfer-trfirst00-2" {
		t.Fatalf("after a cut off receive = %q %q %v; want a new attempt", rec.ID, requestID, err)
	}
	if !store.sessions["mer-2"].IsTerminated || !slices.Contains(rt.destroyedIDs, "tmux-mer-2") {
		t.Fatalf("the unfinished session still runs: %+v", store.sessions["mer-2"])
	}

	add("mer-3", "transfer-trfirst00-2", true)
	if rec, _, err := m.receivedSession(ctx, transfer); err != nil || rec.ID != "mer-3" {
		t.Fatalf("after a finished receive = %q %v, want mer-3", rec.ID, err)
	}

	other := transfer
	other.HeadSHA = strings.Repeat("b", 40)
	if _, _, err := m.receivedSession(ctx, other); !errors.Is(err, ErrClientRequestConflict) {
		t.Fatalf("another move with this id = %v, want ErrClientRequestConflict", err)
	}

	rt.destroyErr = errors.New("still running")
	transfer.TransferID = "trfourth0"
	add("mer-4", "transfer-trfourth0", false)
	if _, _, err := m.receivedSession(ctx, transfer); !errors.Is(err, ErrTransferUnconfirmed) {
		t.Fatalf("an unfinished session that will not end = %v, want ErrTransferUnconfirmed", err)
	}
}

func TestLockTransferWaitsItsTurn(t *testing.T) {
	m := &Manager{}
	unlock, err := m.lockTransfer(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.lockTransfer(waiting, "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled waiter = %v", err)
	}
	if other, err := m.lockTransfer(ctx, "b"); err != nil {
		t.Fatal(err)
	} else {
		other()
	}
	got := make(chan error, 1)
	go func() {
		next, err := m.lockTransfer(ctx, "a")
		if err == nil {
			next()
		}
		got <- err
	}()
	unlock()
	if err := <-got; err != nil {
		t.Fatal(err)
	}
}

func TestReceiveTransferWithoutAConversationStartsFromTheNote(t *testing.T) {
	m, store, _, _ := newMoveManager(t)
	agent := &recordingAgent{}
	m.agents = singleAgent{agent: agent}
	origin := "https://github.com/o/r"
	store.projects["web"] = domain.ProjectRecord{ID: "web", Path: "/repo/web", RepoOriginURL: origin}
	rec, err := m.ReceiveTransfer(ctx, "web", domain.SessionTransfer{
		TransferID: "trrecv001", RepoKey: domain.RepositoryKey(origin), Branch: "feat/x", HeadSHA: strings.Repeat("a", 40),
		Harness: domain.HarnessCodex, HandoffNote: "NOTE",
	}, "resume where you stopped")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Harness != domain.HarnessCodex || agent.lastLaunch.Prompt != "NOTE" {
		t.Fatalf("received %s with prompt %q; want codex with the handoff note", rec.Harness, agent.lastLaunch.Prompt)
	}
}
