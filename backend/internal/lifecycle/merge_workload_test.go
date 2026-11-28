package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// workloadTerminator is a completion terminator that can also count the
// commands a session's agent still runs in its terminal.
type workloadTerminator struct {
	fakeCompletionTerminator
	commands int
	err      error
	counted  []domain.SessionID
}

func (w *workloadTerminator) RunningWorkloadCommands(_ context.Context, id domain.SessionID) (int, error) {
	w.counted = append(w.counted, id)
	return w.commands, w.err
}

func idleMergedSession(st *fakeStore) domain.SessionRecord {
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityIdle
	rec.TerminateOnPRMerge = true
	st.sessions[rec.ID] = rec
	st.prs[rec.ID] = []domain.PullRequest{{URL: "pr1", Merged: true}}
	return rec
}

func TestPRObservation_MergedWaitsForRunningTerminalCommand(t *testing.T) {
	m, st, _ := newManager()
	terminator := &workloadTerminator{commands: 1}
	m.SetCompletionTerminator(terminator)
	rec := idleMergedSession(st)
	merged := ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}

	if err := m.ApplyPRObservation(ctx, rec.ID, merged); err != nil {
		t.Fatal(err)
	}
	if terminator.calls != 0 || st.sessions[rec.ID].IsTerminated {
		t.Fatalf("kill calls = %d, terminated = %v; want the session kept while a command runs",
			terminator.calls, st.sessions[rec.ID].IsTerminated)
	}
	if len(terminator.counted) != 1 || terminator.counted[0] != rec.ID {
		t.Fatalf("counted = %v, want one count for %s", terminator.counted, rec.ID)
	}

	// The command finishes; the next observation of the merged PR lands the teardown.
	terminator.commands = 0
	if err := m.ApplyPRObservation(ctx, rec.ID, merged); err != nil {
		t.Fatal(err)
	}
	if terminator.calls != 1 {
		t.Fatalf("kill calls = %d, want 1 once the terminal is quiet", terminator.calls)
	}
}

func TestPRObservation_MergedTerminatesWhenCommandCountFails(t *testing.T) {
	m, st, _ := newManager()
	terminator := &workloadTerminator{commands: 3, err: errors.New("exact supervisor is not running")}
	m.SetCompletionTerminator(terminator)
	rec := idleMergedSession(st)

	if err := m.ApplyPRObservation(ctx, rec.ID, ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if terminator.calls != 1 {
		t.Fatalf("kill calls = %d, want 1: a failed count must not hold the session open", terminator.calls)
	}
}

func TestPRObservation_MergedDoesNotCountCommandsBeforeSessionIsComplete(t *testing.T) {
	m, st, _ := newManager()
	terminator := &workloadTerminator{commands: 1}
	m.SetCompletionTerminator(terminator)
	rec := idleMergedSession(st)
	st.prs[rec.ID] = append(st.prs[rec.ID], domain.PullRequest{URL: "pr2"})

	if err := m.ApplyPRObservation(ctx, rec.ID, ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if len(terminator.counted) != 0 || terminator.calls != 0 {
		t.Fatalf("counted = %v, kill calls = %d; want neither while a sibling PR is open",
			terminator.counted, terminator.calls)
	}
}
