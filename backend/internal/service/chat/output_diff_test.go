package chat_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Projection of streamed command output and the turn's running diff.
//
// Against the real SQLite store, like the rest of this file: the interesting
// claims are about what the rows end up looking like after a stream of events, and
// a mock store can only restate the assertion.

// startedCommand is the activity a command's output deltas append to. Output
// arrives against an item id, so the row has to exist first.
func startedCommand(turn, item, summary string) ports.ChatEvent {
	return ports.ChatEvent{
		Kind:           ports.ChatEventActivityStarted,
		ProviderTurnID: turn,
		ProviderItemID: item,
		ActivityKind:   domain.ActivityKindCommand,
		ActivityStatus: domain.ActivityStatusRunning,
		Summary:        summary,
	}
}

func outputDelta(turn, item, delta string) ports.ChatEvent {
	return ports.ChatEvent{
		Kind:           ports.ChatEventCommandOutputDelta,
		ProviderTurnID: turn,
		ProviderItemID: item,
		Delta:          delta,
	}
}

func findActivity(t *testing.T, s store.ConversationSnapshot, item string) domain.ConversationActivity {
	t.Helper()
	for _, activity := range s.Activities {
		if activity.ProviderItemID == item {
			return activity
		}
	}
	t.Fatalf("no activity for item %q in %d activities", item, len(s.Activities))
	return domain.ConversationActivity{}
}

// Deltas accumulate onto the command's own row. The alternative -- a timeline
// entry per delta -- would bury the conversation under one chatty command.
func TestCommandOutputDeltasAccumulateOntoOneActivity(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "run the tests", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		startedCommand("provider-turn-1", "exec-1", "go test ./..."),
		outputDelta("provider-turn-1", "exec-1", "ok  pkg/a\n"),
		outputDelta("provider-turn-1", "exec-1", "ok  pkg/b\n"),
		outputDelta("provider-turn-1", "exec-1", "ok  pkg/c\n"),
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, a := range s.Activities {
			if a.ProviderItemID == "exec-1" && strings.Contains(a.CommandOutput, "pkg/c") {
				return true
			}
		}
		return false
	})

	// One row, not four.
	if len(snapshot.Activities) != 1 {
		t.Fatalf("activities = %d, want 1: %+v", len(snapshot.Activities), snapshot.Activities)
	}
	activity := snapshot.Activities[0]
	if got, want := activity.CommandOutput, "ok  pkg/a\nok  pkg/b\nok  pkg/c\n"; got != want {
		t.Errorf("accumulated output = %q, want %q", got, want)
	}
	if activity.CommandOutputTruncated {
		t.Error("short output reported as truncated")
	}
	// The row is still the running command, and a reader must be able to show
	// output for it: an activity that never completes has no aggregate at all.
	if activity.Status != domain.ActivityStatusRunning {
		t.Errorf("status = %q, want running", activity.Status)
	}
	if activity.Revision == 0 {
		t.Error("revision never advanced, so a client cannot tell the output grew")
	}
}

// Some providers report turn/interrupted after killing a process but omit the
// command's item/completed notification. The turn is the authoritative terminal
// boundary, so its command must stop spinning even when that item event is lost.
func TestInterruptedTurnCancelsItsStillRunningActivities(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "run something slow", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		startedCommand("provider-turn-1", "exec-1", "sleep 60"),
		outputDelta("provider-turn-1", "exec-1", "started\n"),
		ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
			TurnState: domain.TurnStateInterrupted,
		},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 1 && len(s.Turns) == 1 &&
			s.Turns[0].State.Terminal()
	})
	if snapshot.Turns[0].State != domain.TurnStateInterrupted {
		t.Errorf("turn state = %q, want interrupted", snapshot.Turns[0].State)
	}
	activity := snapshot.Activities[0]
	if activity.Status != domain.ActivityStatusCancelled {
		t.Errorf("activity status = %q, want cancelled", activity.Status)
	}
	if activity.CommandOutput != "started\n" {
		t.Errorf("command output = %q, want the output received before stop", activity.CommandOutput)
	}

	// A killed process may report its own completion after the provider has already
	// acknowledged the interrupt. That late item cannot resurrect stopped work.
	h.conv.emit(
		ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "exec-1", ActivityKind: domain.ActivityKindCommand,
			ActivityStatus: domain.ActivityStatusCompleted, Summary: "sleep 60",
		},
		ports.ChatEvent{
			Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "late-marker", Text: "late event projected",
		},
	)
	snapshot = h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) == 2
	})
	if got := findActivity(t, snapshot, "exec-1").Status; got != domain.ActivityStatusCancelled {
		t.Errorf("activity status after late completion = %q, want cancelled", got)
	}
}

// Output is capped, and the cap is stated rather than applied silently. A build
// that prints for an hour must not put megabytes into a row every snapshot reads.
func TestCommandOutputIsCappedAndSaysSo(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "build it", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		startedCommand("provider-turn-1", "exec-1", "make"),
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Activities) == 1 })

	// Comfortably past the cap, in chunks, the way a real runaway command arrives.
	chunk := strings.Repeat("x", 8*1024)
	for i := 0; i < 16; i++ {
		h.conv.emit(outputDelta("provider-turn-1", "exec-1", chunk))
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, a := range s.Activities {
			if a.ProviderItemID == "exec-1" && a.CommandOutputTruncated {
				return true
			}
		}
		return false
	})

	activity := findActivity(t, snapshot, "exec-1")
	if len(activity.CommandOutput) != store.MaxCommandOutputChars {
		t.Errorf("stored output = %d chars, want exactly the cap %d",
			len(activity.CommandOutput), store.MaxCommandOutputChars)
	}
	if !activity.CommandOutputTruncated {
		t.Error("output was cut without the row saying so")
	}
}

// A delta whose activity does not exist is dropped, not turned into a row of its
// own. The provider can emit output before the item/started that names the command,
// and an activity invented from a delta would have no command on it.
func TestOutputDeltaForUnknownActivityIsDropped(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "go", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		outputDelta("provider-turn-1", "exec-orphan", "output with no command\n"),
		// A later real activity proves the projector kept running rather than
		// stopping on the orphan.
		startedCommand("provider-turn-1", "exec-1", "ls"),
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 1
	})
	if got := snapshot.Activities[0].ProviderItemID; got != "exec-1" {
		t.Errorf("surviving activity = %q, want the real command", got)
	}
	if snapshot.Activities[0].CommandOutput != "" {
		t.Errorf("orphan output leaked onto another activity: %q",
			snapshot.Activities[0].CommandOutput)
	}
}

// The turn diff is per-turn state. The provider re-sends it whole on every update,
// so repeats overwrite and never stack up as timeline entries.
func TestTurnDiffOverwritesAndAddsNoTimelineRows(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "edit two files", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	first := ports.ChatEvent{
		Kind:           ports.ChatEventTurnDiff,
		ProviderTurnID: "provider-turn-1",
		Diff: &ports.ChatTurnDiff{Files: []ports.ChatDiffFile{
			{Path: "a.txt", Additions: 1, Deletions: 0, Status: "modified"},
		}},
	}
	grown := ports.ChatEvent{
		Kind:           ports.ChatEventTurnDiff,
		ProviderTurnID: "provider-turn-1",
		Diff: &ports.ChatTurnDiff{Files: []ports.ChatDiffFile{
			{Path: "a.txt", Additions: 3, Deletions: 1, Status: "modified"},
			{Path: "b.txt", Additions: 5, Deletions: 0, Status: "added"},
		}},
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		first,
		// The same payload twice, exactly as the provider was observed sending it.
		grown,
		grown,
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].Diff != nil && len(s.Turns[0].Diff.Files) == 2
	})

	// Three diff notifications, zero timeline entries.
	if len(snapshot.Activities) != 0 {
		t.Fatalf("turn diffs became %d timeline activities: %+v",
			len(snapshot.Activities), snapshot.Activities)
	}

	diff := snapshot.Turns[0].Diff
	if diff.Truncated {
		t.Error("small diff reported as truncated")
	}
	// The latest payload wins whole: the counts from the first update must be gone
	// rather than merged with the second.
	if diff.Files[0].Path != "a.txt" || diff.Files[0].Additions != 3 || diff.Files[0].Deletions != 1 {
		t.Errorf("first file = %+v, want the latest counts", diff.Files[0])
	}
	if diff.Files[1].Path != "b.txt" || diff.Files[1].Status != "added" {
		t.Errorf("second file = %+v", diff.Files[1])
	}
}

// A provider edit outside the worktree, such as the memory file Claude keeps
// under its own home directory, is still part of the turn, but the Files panel
// cannot open it. The diff says which rows those are so a client does not offer
// a link that leads nowhere.
func TestTurnDiffMarksFilesOutsideTheWorkspace(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "remember this", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	inside := filepath.Join(h.workspace, "docs", "plan.md")
	memory := filepath.Join(t.TempDir(), ".claude", "projects", "demo", "memory", "rate-limiting-deferred.md")
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{
			Kind:           ports.ChatEventTurnDiff,
			ProviderTurnID: "provider-turn-1",
			Diff: &ports.ChatTurnDiff{Files: []ports.ChatDiffFile{
				{Path: inside, Additions: 2, Status: "added"},
				{Path: "src/a.go", Additions: 1, Status: "modified"},
				{Path: memory, Additions: 11, Status: "added"},
			}},
		},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].Diff != nil && len(s.Turns[0].Diff.Files) == 3
	})
	outside := map[string]bool{}
	for _, file := range snapshot.Turns[0].Diff.Files {
		outside[file.Path] = file.OutsideWorkspace
	}
	if outside[inside] || outside["src/a.go"] {
		t.Errorf("worktree files marked outside: %v", outside)
	}
	if !outside[memory] {
		t.Errorf("memory file outside the worktree not marked: %v", outside)
	}
}

// A driver that had to cut the file list says so, and the flag survives the round
// trip through storage: a partial list presented as whole understates the change.
func TestTruncatedTurnDiffKeepsItsFlag(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "regenerate everything", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{
			Kind:           ports.ChatEventTurnDiff,
			ProviderTurnID: "provider-turn-1",
			Summary:        domain.ChatDiffTruncatedSummary,
			Diff: &ports.ChatTurnDiff{Files: []ports.ChatDiffFile{
				{Path: "gen/a.go", Additions: 10, Status: "added"},
			}},
		},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].Diff != nil
	})
	if !snapshot.Turns[0].Diff.Truncated {
		t.Error("truncation marker did not survive into the stored diff")
	}
}

// An empty diff clears a stale file list. A turn that reverted its own edits has
// changed nothing, and the previous answer must not linger.
func TestEmptyTurnDiffClearsThePreviousOne(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "try then revert", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{
			Kind: ports.ChatEventTurnDiff, ProviderTurnID: "provider-turn-1",
			Diff: &ports.ChatTurnDiff{Files: []ports.ChatDiffFile{
				{Path: "scratch.txt", Additions: 4, Status: "added"},
			}},
		},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].Diff != nil && len(s.Turns[0].Diff.Files) == 1
	})

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnDiff, ProviderTurnID: "provider-turn-1",
		Diff: &ports.ChatTurnDiff{},
	})

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].Diff != nil && len(s.Turns[0].Diff.Files) == 0
	})
	if files := snapshot.Turns[0].Diff.Files; len(files) != 0 {
		t.Errorf("stale file list survived a revert: %+v", files)
	}
}

// A diff naming a turn AO never recorded is dropped rather than attributed to
// whichever turn happened to be nearby. This is the post-restart case, where a
// controller reattaches to a provider turn that predates it.
func TestTurnDiffForUnknownTurnIsDropped(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "hello", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{
			Kind: ports.ChatEventTurnDiff, ProviderTurnID: "turn-from-a-previous-life",
			Diff: &ports.ChatTurnDiff{Files: []ports.ChatDiffFile{
				{Path: "elsewhere.txt", Additions: 9, Status: "modified"},
			}},
		},
		// Ordered after, so waiting on it proves the diff was already handled.
		ports.ChatEvent{
			Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "msg-1", Text: "done",
		},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) == 2
	})
	if len(snapshot.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snapshot.Turns))
	}
	if diff := snapshot.Turns[0].Diff; diff != nil {
		t.Errorf("another turn's diff was attributed to this one: %+v", diff)
	}
}
