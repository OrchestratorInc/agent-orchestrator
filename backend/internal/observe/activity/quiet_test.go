package activity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// quietRuntime has a rendered viewport and counts the commands under the agent.
type quietRuntime struct {
	fakeRuntime
	rendered    string
	renderedErr error
	commands    int
	commandsErr error
}

func (r *quietRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return r.rendered, r.renderedErr
}

func (r *quietRuntime) RunningWorkloadCommands(context.Context, ports.RuntimeHandle, ports.SupervisedProcessRef) (int, error) {
	return r.commands, r.commandsErr
}

// frozenScreen is a prompt left on screen after a turn ended without its Stop
// hook. Droid has no activity detector, so nothing here is parsed.
const frozenScreen = ">\n  ? for help · ctrl+c to exit"

func newQuietObserver(session domain.SessionRecord, sink *fakeSink, runtime outputReader, agent ports.Agent, clock *time.Time) *Observer {
	return New(fakeSessions{rows: []domain.SessionRecord{session}}, sink, runtime, fakeAgents{session.Harness: agent},
		Config{Clock: func() time.Time { return *clock }, Logger: testLogger()})
}

// pollEvery polls once per observer tick from start through end, running each
// first so a test can change the screen between ticks.
func pollEvery(t *testing.T, observer *Observer, clock *time.Time, start, end time.Time, each func(time.Time)) {
	t.Helper()
	for at := start; !at.After(end); at = at.Add(DefaultTickInterval) {
		*clock = at
		if each != nil {
			each(at)
		}
		if err := observer.Poll(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPollMovesFrozenTerminalWithoutDetectorToWaitingInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime *quietRuntime
	}{
		{"rendered viewport", &quietRuntime{rendered: frozenScreen}},
		{"detached host without rendered capture", &quietRuntime{fakeRuntime: fakeRuntime{output: frozenScreen}, renderedErr: ports.ErrStyledTerminalOutputUnavailable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(500, 0).UTC()
			clock := start
			session := activeSession(start, domain.HarnessDroid)
			session.Revision = 7
			sink := &fakeSink{}
			observer := newQuietObserver(session, sink, tc.runtime, droid.New(), &clock)

			pollEvery(t, observer, &clock, start, start.Add(quietAfter-DefaultTickInterval), nil)
			if len(sink.signals) != 0 {
				t.Fatalf("moved before the screen was quiet for %s: %+v", quietAfter, sink.signals)
			}
			pollEvery(t, observer, &clock, start.Add(quietAfter), start.Add(quietAfter), nil)
			if len(sink.signals) != 1 {
				t.Fatalf("signals = %+v, want one", sink.signals)
			}
			got := sink.signals[0]
			if !got.Valid || got.State != domain.ActivityWaitingInput || got.Event != "terminal-quiet" || !got.Timestamp.Equal(start.Add(quietAfter)) ||
				got.ExpectedRevision == nil || *got.ExpectedRevision != 7 || got.LaunchID != "launch-1" {
				t.Fatalf("signal = %+v", got)
			}
		})
	}
}

func TestPollKeepsAnimatedTerminalWithoutDetectorActive(t *testing.T) {
	start := time.Unix(500, 0).UTC()
	clock := start
	runtime := &quietRuntime{}
	sink := &fakeSink{}
	observer := newQuietObserver(activeSession(start, domain.HarnessDroid), sink, runtime, droid.New(), &clock)

	// A long turn with no hooks: only its timer moves.
	pollEvery(t, observer, &clock, start, start.Add(3*quietAfter), func(at time.Time) {
		runtime.rendered = fmt.Sprintf("⠋ Thinking… (%ds · esc to interrupt)\n>", int(at.Sub(start).Seconds())+180)
	})
	if len(sink.signals) != 0 {
		t.Fatalf("an animating turn left Working: %+v", sink.signals)
	}
}

func TestPollRestartsQuietWindowWhileACommandRuns(t *testing.T) {
	start := time.Unix(500, 0).UTC()
	clock := start
	runtime := &quietRuntime{rendered: frozenScreen, commands: 1}
	sink := &fakeSink{}
	observer := newQuietObserver(activeSession(start, domain.HarnessDroid), sink, runtime, droid.New(), &clock)

	// A silent test run under a TUI that draws nothing while it waits.
	lastBusy := start.Add(2 * quietAfter)
	pollEvery(t, observer, &clock, start, lastBusy, nil)
	runtime.commands = 0
	pollEvery(t, observer, &clock, lastBusy.Add(DefaultTickInterval), lastBusy.Add(quietAfter-DefaultTickInterval), nil)
	if len(sink.signals) != 0 {
		t.Fatalf("moved during or right after a running command: %+v", sink.signals)
	}
	pollEvery(t, observer, &clock, lastBusy.Add(quietAfter), lastBusy.Add(quietAfter), nil)
	if len(sink.signals) != 1 || sink.signals[0].State != domain.ActivityWaitingInput {
		t.Fatalf("signals = %+v, want waiting_input a full window after the command", sink.signals)
	}
}

func TestPollRestartsQuietWindowOnRelaunch(t *testing.T) {
	start := time.Unix(500, 0).UTC()
	clock := start
	session := activeSession(start, domain.HarnessDroid)
	sink := &fakeSink{}
	observer := newQuietObserver(session, sink, &quietRuntime{rendered: frozenScreen}, droid.New(), &clock)

	relaunch := start.Add(quietAfter - DefaultTickInterval)
	pollEvery(t, observer, &clock, start, relaunch.Add(-DefaultTickInterval), nil)
	session.Metadata.RuntimeLaunchID = "launch-2"
	observer.sessions = fakeSessions{rows: []domain.SessionRecord{session}}
	pollEvery(t, observer, &clock, relaunch, relaunch.Add(quietAfter-DefaultTickInterval), nil)
	if len(sink.signals) != 0 {
		t.Fatalf("a relaunched agent inherited the old screen's quiet time: %+v", sink.signals)
	}
	pollEvery(t, observer, &clock, relaunch.Add(quietAfter), relaunch.Add(quietAfter), nil)
	if len(sink.signals) != 1 || sink.signals[0].LaunchID != "launch-2" {
		t.Fatalf("signals = %+v, want one for launch-2", sink.signals)
	}
}

func TestPollNeverMovesTerminalWithoutDetectorOnMissingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session func(*domain.SessionRecord)
		runtime outputReader
	}{
		{"running commands unknown", nil, &quietRuntime{rendered: frozenScreen, commandsErr: errors.New("exact supervisor is not running")}},
		{"runtime cannot count commands", nil, &fakeRuntime{output: frozenScreen}},
		{"blank screen", nil, &quietRuntime{rendered: " \n\n"}},
		{"rendered capture failed", nil, &quietRuntime{fakeRuntime: fakeRuntime{output: frozenScreen}, renderedErr: errors.New("capture failed")}},
		{"chat session", func(s *domain.SessionRecord) { s.Mode = domain.SessionModeChat }, &quietRuntime{rendered: frozenScreen}},
		{"already waiting on the user", func(s *domain.SessionRecord) { s.Activity.State = domain.ActivityWaitingInput }, &quietRuntime{rendered: frozenScreen}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(500, 0).UTC()
			clock := start
			session := activeSession(start, domain.HarnessDroid)
			if tc.session != nil {
				tc.session(&session)
			}
			sink := &fakeSink{}
			observer := newQuietObserver(session, sink, tc.runtime, droid.New(), &clock)
			pollEvery(t, observer, &clock, start, start.Add(2*quietAfter), nil)
			if len(sink.signals) != 0 {
				t.Fatalf("moved without evidence: %+v", sink.signals)
			}
		})
	}
}

func TestPollReadsRenderedSurfaceOfInspectorAdapters(t *testing.T) {
	// Antigravity CLI 1.3.4 frames, as in agy's inspector tests.
	rule := strings.Repeat("─", 60)
	model := "                     Gemini 3.8 Flash · medium"
	idle := strings.Join([]string{rule, ">", rule, "? for shortcuts" + model}, "\n")
	working := strings.Join([]string{"⡿  Generating...", rule, ">", rule, "esc to cancel" + model}, "\n")
	approval := strings.Join([]string{rule, "Run this command?", "> 1. Yes, run command", "  4. No, cancel", "  ↑/↓ Navigate · tab Amend", "esc to cancel" + model}, "\n")
	draft := strings.Join([]string{rule, "> retry the migration", rule, model}, "\n")
	for _, tc := range []struct {
		name    string
		runtime outputReader
		state   domain.ActivityState
		event   string
		after   time.Duration
	}{
		{"idle composer after Esc", &quietRuntime{rendered: idle}, domain.ActivityIdle, "terminal-idle", 0},
		{"approval dialog", &quietRuntime{rendered: approval}, domain.ActivityWaitingInput, "terminal-waiting-input", 0},
		{"working frame never goes quiet", &quietRuntime{rendered: working}, "", "", 0},
		{"unrecognized frame falls back to the quiet screen", &quietRuntime{rendered: draft}, domain.ActivityWaitingInput, "terminal-quiet", quietAfter},
		// Antigravity's raw stream never forms a composer, so it is not evidence.
		{"raw output is not inspected", &fakeRuntime{output: idle}, "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(500, 0).UTC()
			clock := start
			sink := &fakeSink{}
			observer := newQuietObserver(activeSession(start, domain.HarnessAgy), sink, tc.runtime, agy.New(), &clock)
			pollEvery(t, observer, &clock, start, start.Add(quietAfter), nil)
			if tc.state == "" {
				if len(sink.signals) != 0 {
					t.Fatalf("signals = %+v, want none", sink.signals)
				}
				return
			}
			if len(sink.signals) == 0 {
				t.Fatal("no signal")
			}
			got := sink.signals[0]
			if got.State != tc.state || got.Event != tc.event || !got.Timestamp.Equal(start.Add(tc.after)) {
				t.Fatalf("first signal = %+v, want %s/%s at +%s", got, tc.state, tc.event, tc.after)
			}
		})
	}
}
