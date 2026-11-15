package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
)

type fakeTerminalForkSources struct {
	source sessionfork.Source
	output string
}

func (f fakeTerminalForkSources) TerminalForkSource(context.Context, domain.SessionRecord, string) (sessionfork.Source, error) {
	return f.source, nil
}

func (f fakeTerminalForkSources) ForkTerminalOutput(context.Context, domain.SessionRecord) string {
	return f.output
}

type recordingForkNotes struct {
	source   domain.SessionID
	target   domain.SessionID
	terminal string
}

func (r *recordingForkNotes) ForkHandoffNote(_ context.Context, source domain.SessionRecord, target ports.ForkTarget, terminalOutput string, _ time.Time) (string, error) {
	r.source, r.target, r.terminal = source.ID, target.SessionID, terminalOutput
	return "NOTE", nil
}

// A terminal agent with no fork command is forked with its conversation, and
// the end of its screen, given to a fresh agent as text.
func TestTerminalAgentWithoutForkCommandForksAsText(t *testing.T) {
	ctx := context.Background()
	rec := domain.SessionRecord{ID: "copilot-1", Mode: domain.SessionModeTUI, Harness: domain.HarnessCopilot}
	for _, refusal := range []string{sessionfork.CodeAgentUnsupported, sessionfork.CodeNativeMissing} {
		notes := &recordingForkNotes{}
		sources := forkSources{terminal: fakeTerminalForkSources{source: sessionfork.Source{Refusal: refusal}, output: "$ ls"}, notes: notes}
		source, err := sources.ForkSource(ctx, rec, "")
		if err != nil {
			t.Fatal(err)
		}
		if source.Refusal != "" || source.ContextMode != domain.ForkContextTextHandoff || source.Handoff == nil {
			t.Fatalf("%s: source = %+v, want a text handoff", refusal, source)
		}
		note, err := source.Handoff(ctx, ports.ForkTarget{SessionID: "fork-1"})
		if err != nil || note != "NOTE" {
			t.Fatalf("%s: handoff = %q, %v", refusal, note, err)
		}
		if notes.source != "copilot-1" || notes.target != "fork-1" || notes.terminal != "$ ls" {
			t.Fatalf("%s: note built from %+v, want the original, the fork, and its screen", refusal, notes)
		}
	}
}

// Other refusals still stand: a text copy can't fix them.
func TestTerminalForkKeepsRefusalsTextCantFix(t *testing.T) {
	rec := domain.SessionRecord{ID: "copilot-1", Mode: domain.SessionModeTUI}
	sources := forkSources{terminal: fakeTerminalForkSources{source: sessionfork.Source{Refusal: sessionfork.CodeOlderTurn}}, notes: &recordingForkNotes{}}
	source, err := sources.ForkSource(context.Background(), rec, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if source.Refusal != sessionfork.CodeOlderTurn {
		t.Fatalf("refusal = %q, want %q", source.Refusal, sessionfork.CodeOlderTurn)
	}
}

// A hibernated Chat session gets the notice queued for when its agent wakes,
// rather than being woken for it.
func TestPRMovedNoticeIsQueuedForAHibernatedChat(t *testing.T) {
	var queued string
	send := func(context.Context, domain.SessionID, string) error {
		return fmt.Errorf("send x: %w", chatsvc.ErrNoController)
	}
	queue := func(_ context.Context, _ domain.SessionID, text string) error { queued = text; return nil }
	resume := func(context.Context, domain.SessionID) error {
		t.Fatal("a hibernated chat must not be woken")
		return nil
	}
	if err := tellPRMoved(context.Background(), send, queue, resume, "fork-1", "PR #1 moved"); err != nil {
		t.Fatal(err)
	}
	if queued != "PR #1 moved" {
		t.Fatalf("queued = %q, want the notice", queued)
	}
}

// A Chat session whose agent never started has nowhere to queue the notice,
// so its agent is started, as opening the session would, and told.
func TestPRMovedNoticeStartsAChatAgentThatNeverStarted(t *testing.T) {
	var started bool
	var sent string
	send := func(_ context.Context, _ domain.SessionID, text string) error {
		if !started {
			return fmt.Errorf("send x: %w", chatsvc.ErrNoController)
		}
		sent = text
		return nil
	}
	queue := func(context.Context, domain.SessionID, string) error { return chatsvc.ErrNotProvisioning }
	resume := func(context.Context, domain.SessionID) error { started = true; return nil }
	if err := tellPRMoved(context.Background(), send, queue, resume, "fork-1", "PR #1 moved"); err != nil {
		t.Fatal(err)
	}
	if !started || sent != "PR #1 moved" {
		t.Fatalf("started = %v, sent = %q; want the agent started and told", started, sent)
	}
}

func TestPRMovedNoticeIsSentToARunningAgent(t *testing.T) {
	var sent string
	send := func(_ context.Context, _ domain.SessionID, text string) error { sent = text; return nil }
	queue := func(context.Context, domain.SessionID, string) error { t.Fatal("must not queue"); return nil }
	resume := func(context.Context, domain.SessionID) error {
		t.Fatal("a running agent must not be restarted")
		return nil
	}
	if err := tellPRMoved(context.Background(), send, queue, resume, "fork-1", "PR #1 moved"); err != nil || sent != "PR #1 moved" {
		t.Fatalf("sent = %q, %v", sent, err)
	}
}

type changingTerminal struct{ screen *string }

func (c changingTerminal) TerminalForkSource(context.Context, domain.SessionRecord, string) (sessionfork.Source, error) {
	return sessionfork.Source{Refusal: sessionfork.CodeAgentUnsupported}, nil
}
func (c changingTerminal) ForkTerminalOutput(context.Context, domain.SessionRecord) string {
	return *c.screen
}

type cutoffNotes struct {
	screen string
	upTo   time.Time
}

func (n *cutoffNotes) ForkHandoffNote(_ context.Context, _ domain.SessionRecord, _ ports.ForkTarget, terminalOutput string, upTo time.Time) (string, error) {
	n.screen, n.upTo = terminalOutput, upTo
	return "NOTE", nil
}

// A terminal fork's note is the screen and turns as they were when its files
// were captured, not whatever the original did while the fork was set up.
func TestTerminalTextForkUsesTheScreenFromWhenItsFilesWereCaptured(t *testing.T) {
	screen := "$ before the fork"
	notes := &cutoffNotes{}
	sources := forkSources{terminal: changingTerminal{screen: &screen}, notes: notes}
	source, err := sources.ForkSource(context.Background(), domain.SessionRecord{ID: "copilot-1", Mode: domain.SessionModeTUI}, "")
	if err != nil || source.Freeze == nil {
		t.Fatalf("source = %+v, %v; want a freeze hook", source, err)
	}
	before := time.Now()
	source.Freeze(context.Background())
	screen = "$ a later command"
	if _, err := source.Handoff(context.Background(), ports.ForkTarget{SessionID: "fork-1"}); err != nil {
		t.Fatal(err)
	}
	if notes.screen != "$ before the fork" {
		t.Fatalf("screen = %q, want the one from when the files were captured", notes.screen)
	}
	if notes.upTo.Before(before) || notes.upTo.After(time.Now()) {
		t.Fatalf("turns cut off at %v, want when the files were captured", notes.upTo)
	}
}
