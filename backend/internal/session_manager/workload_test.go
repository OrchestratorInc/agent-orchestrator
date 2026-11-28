package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// countingWorkloadRuntime reports a fixed number of running terminal commands
// and records which launch it was asked about.
type countingWorkloadRuntime struct {
	*fakeRuntime
	commands int
	err      error
	refs     []ports.SupervisedProcessRef
	handles  []ports.RuntimeHandle
}

func (r *countingWorkloadRuntime) RunningWorkloadCommands(_ context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (int, error) {
	r.handles = append(r.handles, handle)
	r.refs = append(r.refs, ref)
	return r.commands, r.err
}

func seedTerminalSession(store *fakeStore) domain.SessionRecord {
	rec := domain.SessionRecord{
		ID: "mer-1", ProjectID: chatTestProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1",
			RuntimeHandleID: "pane-mer-1", RuntimeLaunchID: "launch-1",
		},
	}
	store.sessions[rec.ID] = rec
	return rec
}

func TestRunningWorkloadCommandsCountsTheSessionsExactLaunch(t *testing.T) {
	mgr, store, rt := newChatManager(&recordingLauncher{})
	counter := &countingWorkloadRuntime{fakeRuntime: rt, commands: 2}
	mgr.runtime = counter
	rec := seedTerminalSession(store)

	got, err := mgr.RunningWorkloadCommands(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("commands = %d, want 2", got)
	}
	if len(counter.refs) != 1 || counter.refs[0].SessionID != rec.ID || counter.refs[0].LaunchID != "launch-1" {
		t.Fatalf("refs = %+v, want the session's launch-1", counter.refs)
	}
	if counter.handles[0].ID != "pane-mer-1" {
		t.Fatalf("handle = %+v, want pane-mer-1", counter.handles[0])
	}
}

func TestRunningWorkloadCommandsReportsNoneWithoutATerminal(t *testing.T) {
	for name, change := range map[string]func(*domain.SessionRecord){
		"chat":       func(r *domain.SessionRecord) { r.Mode = domain.SessionModeChat },
		"terminated": func(r *domain.SessionRecord) { r.IsTerminated = true },
	} {
		t.Run(name, func(t *testing.T) {
			mgr, store, rt := newChatManager(&recordingLauncher{})
			counter := &countingWorkloadRuntime{fakeRuntime: rt, commands: 2}
			mgr.runtime = counter
			rec := seedTerminalSession(store)
			change(&rec)
			store.sessions[rec.ID] = rec

			got, err := mgr.RunningWorkloadCommands(context.Background(), rec.ID)
			if err != nil || got != 0 {
				t.Fatalf("commands = %d, err = %v; want 0, nil", got, err)
			}
			if len(counter.refs) != 0 {
				t.Fatalf("runtime was asked %d times, want never", len(counter.refs))
			}
		})
	}
}

func TestRunningWorkloadCommandsErrorsWhenTheCountCannotBeProved(t *testing.T) {
	t.Run("runtime cannot count", func(t *testing.T) {
		mgr, store, _ := newChatManager(&recordingLauncher{})
		rec := seedTerminalSession(store)
		if _, err := mgr.RunningWorkloadCommands(context.Background(), rec.ID); !errors.Is(err, errWorkloadUninspectable) {
			t.Fatalf("err = %v, want errWorkloadUninspectable", err)
		}
	})
	t.Run("count fails", func(t *testing.T) {
		mgr, store, rt := newChatManager(&recordingLauncher{})
		probeErr := errors.New("process table unavailable")
		mgr.runtime = &countingWorkloadRuntime{fakeRuntime: rt, err: probeErr}
		rec := seedTerminalSession(store)
		if _, err := mgr.RunningWorkloadCommands(context.Background(), rec.ID); !errors.Is(err, probeErr) {
			t.Fatalf("err = %v, want the probe error", err)
		}
	})
	t.Run("unknown session", func(t *testing.T) {
		mgr, _, _ := newChatManager(&recordingLauncher{})
		if _, err := mgr.RunningWorkloadCommands(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}
