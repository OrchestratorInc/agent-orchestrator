package activity

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/continueagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/crush"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kiro"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/memcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSessions struct {
	rows []domain.SessionRecord
	err  error
}

func (f fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return f.rows, f.err
}

type fakeSink struct {
	id      domain.SessionID
	signals []ports.ActivitySignal
	applied func()
	err     error
}

func (f *fakeSink) ApplyActivitySignal(_ context.Context, id domain.SessionID, signal ports.ActivitySignal) error {
	f.id = id
	f.signals = append(f.signals, signal)
	if f.err != nil {
		return f.err
	}
	if f.applied != nil {
		f.applied()
	}
	return nil
}

type pollingResolverAgent struct {
	ports.Agent
	state        domain.ActivityState
	resolvedID   string
	resolveErr   error
	resolveCalls int
}

func (a *pollingResolverAgent) DetectTerminalActivity(string) (domain.ActivityState, bool) {
	return a.state, true
}
func (*pollingResolverAgent) ContinuouslyDetectTerminalActivity() bool { return true }
func (a *pollingResolverAgent) ResolveNativeSessionID(context.Context, ports.NativeSessionResolveConfig) (string, bool, error) {
	a.resolveCalls++
	return a.resolvedID, true, a.resolveErr
}

func TestPollOnlyResolvesOrdinaryNativeIdentityOnStateChange(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	session := activeSession(now, domain.HarnessCodewhale)
	agent := &pollingResolverAgent{state: domain.ActivityActive, resolvedID: "native-id"}
	sink := &fakeSink{}
	observer := New(fakeSessions{rows: []domain.SessionRecord{session}}, sink, &fakeRuntime{}, fakeAgents{session.Harness: agent}, Config{DataDir: t.TempDir(), Clock: func() time.Time { return now }, Logger: testLogger()})
	if err := observer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if agent.resolveCalls != 0 || len(sink.signals) != 0 {
		t.Fatalf("unchanged state scanned native identity: calls=%d signals=%+v", agent.resolveCalls, sink.signals)
	}
	agent.state = domain.ActivityIdle
	if err := observer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if agent.resolveCalls != 1 || len(sink.signals) != 1 || sink.signals[0].AgentSessionID != "native-id" {
		t.Fatalf("state change missed native identity: calls=%d signals=%+v", agent.resolveCalls, sink.signals)
	}
}

type renderedResolverAgent struct{ *pollingResolverAgent }

func (renderedResolverAgent) RequiresRenderedActivity() bool { return true }

func TestPollReconcilesActivityWhenNativeIdentityResolutionFails(t *testing.T) {
	for _, tt := range []struct {
		name          string
		current, next domain.ActivityState
		event         string
	}{
		{"active to idle", domain.ActivityActive, domain.ActivityIdle, "terminal-idle"},
		{"blocked to idle", domain.ActivityBlocked, domain.ActivityIdle, "permission-resolved"},
		{"blocked to active", domain.ActivityBlocked, domain.ActivityActive, "permission-resolved"},
		{"blocked to waiting", domain.ActivityBlocked, domain.ActivityWaitingInput, "permission-resolved"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(500, 0).UTC()
			session := activeSession(now, domain.HarnessHermes)
			session.Activity.State = tt.current
			session.Metadata.AgentSessionID = "previous-native-id"
			agent := &pollingResolverAgent{
				state:      tt.next,
				resolvedID: "unconfirmed-native-id",
				resolveErr: errors.New("native state unavailable"),
			}
			sink := &fakeSink{}
			observer := New(fakeSessions{rows: []domain.SessionRecord{session}}, sink, &renderedRuntime{}, fakeAgents{session.Harness: renderedResolverAgent{agent}}, Config{DataDir: t.TempDir(), Clock: func() time.Time { return now }, Logger: testLogger()})
			for range 2 {
				if err := observer.Poll(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if len(sink.signals) != 2 || agent.resolveCalls != 2 {
				t.Fatalf("identity failures dropped activity: resolves=%d signals=%+v", agent.resolveCalls, sink.signals)
			}
			for _, signal := range sink.signals {
				if !signal.Valid || signal.State != tt.next || signal.Event != tt.event || signal.AgentSessionID != "" {
					t.Fatalf("activity or unconfirmed identity was projected incorrectly: %+v", signal)
				}
				if signal.ExpectedRevision == nil || *signal.ExpectedRevision != session.Revision || signal.LaunchID != session.Metadata.RuntimeLaunchID {
					t.Fatalf("activity reconciliation lost its fences: %+v", signal)
				}
			}
		})
	}
}

type fakeRuntime struct {
	output string
	err    error
	calls  int
}

func (f *fakeRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	f.calls++
	return f.output, f.err
}

type fakeAgents map[domain.AgentHarness]ports.Agent

func (f fakeAgents) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	agent, ok := f[harness]
	return agent, ok
}

func activeSession(now time.Time, harness domain.AgentHarness) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        "ao-1",
		Harness:   harness,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-3 * time.Minute)},
		UpdatedAt: now.Add(-3 * time.Minute),
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "ao-1",
			RuntimeLaunchID: "launch-1",
		},
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPollReconcilesStaleCodexAtComposer(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	session := activeSession(now, domain.HarnessCodex)
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: "› Write tests for @filename\n\ngpt-5.6-sol low · ~/project\n"}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{session}},
		sink,
		runtime,
		fakeAgents{domain.HarnessCodex: codex.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 {
		t.Fatalf("signals = %d, want 1", len(sink.signals))
	}
	signal := sink.signals[0]
	if sink.id != session.ID || signal.State != domain.ActivityIdle || signal.Event != "terminal-idle" {
		t.Fatalf("unexpected reconciliation: id=%q signal=%+v", sink.id, signal)
	}
	if signal.ExpectedRevision == nil || *signal.ExpectedRevision != session.Revision || signal.LaunchID != "launch-1" {
		t.Fatalf("reconciliation fence = %+v, want revision=%d launch=launch-1", signal, session.Revision)
	}
}

func TestPollKeepsGenuineLongCodexTurnActive(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: "• Working (3m 10s • esc to interrupt)\n› Add tests\n\ngpt-5.6-sol low · ~/project\n"}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{activeSession(now, domain.HarnessCodex)}},
		sink,
		runtime,
		fakeAgents{domain.HarnessCodex: codex.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 0 {
		t.Fatalf("long active turn emitted reconciliation: %+v", sink.signals)
	}
}

func TestPollContinuouslyReconcilesMuse(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	tests := []struct {
		name    string
		current domain.ActivityState
		output  string
		want    domain.ActivityState
		event   string
	}{
		{"fresh active to waiting", domain.ActivityActive, "◆ Request user input AO Muse Fix  1m 02s)\n", domain.ActivityWaitingInput, "terminal-waiting-input"},
		{"waiting to active", domain.ActivityWaitingInput, "◇ Finishing up (25s · esc to interrupt)\n", domain.ActivityActive, "terminal-active"},
		{"idle to waiting", domain.ActivityIdle, "Enter to select · ↑/↓ to move · Tab for an optional note · Esc to interrupt\n", domain.ActivityWaitingInput, "terminal-waiting-input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := activeSession(now, domain.HarnessMuse)
			session.Activity = domain.Activity{State: tt.current, LastActivityAt: now.Add(-time.Second)}
			session.UpdatedAt = now.Add(-time.Second)
			sink := &fakeSink{}
			observer := New(
				fakeSessions{rows: []domain.SessionRecord{session}},
				sink,
				&fakeRuntime{output: tt.output},
				fakeAgents{domain.HarnessMuse: muse.New()},
				Config{Clock: func() time.Time { return now }, Logger: testLogger()},
			)

			if err := observer.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(sink.signals) != 1 || sink.signals[0].State != tt.want || sink.signals[0].Event != tt.event {
				t.Fatalf("unexpected reconciliation: %+v", sink.signals)
			}
		})
	}
}

func TestPollPreservesHookBlockedContinuousDetectors(t *testing.T) {
	for _, tt := range []struct {
		harness domain.AgentHarness
		agent   ports.Agent
	}{
		{domain.HarnessAider, aider.New()},
		{domain.HarnessCline, cline.New()},
		{domain.HarnessContinue, continueagent.New()},
		{domain.HarnessKiro, kiro.New()},
		{domain.HarnessMuse, muse.New()},
		{domain.HarnessFake, unrenderedWaitingAgent{}},
	} {
		t.Run(string(tt.harness), func(t *testing.T) {
			now := time.Unix(500, 0).UTC()
			session := activeSession(now, tt.harness)
			session.Activity.State = domain.ActivityBlocked
			sink := &fakeSink{}
			runtime := &fakeRuntime{output: "◇ Finishing up (25s · esc to interrupt)\n"}
			observer := New(fakeSessions{rows: []domain.SessionRecord{session}}, sink, runtime, fakeAgents{tt.harness: tt.agent}, Config{Clock: func() time.Time { return now }, Logger: testLogger()})
			if err := observer.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			if runtime.calls != 0 || len(sink.signals) != 0 {
				t.Fatalf("hook-owned block was sampled or reconciled: output calls=%d signals=%+v", runtime.calls, sink.signals)
			}
		})
	}
}

func TestPollReconcilesWaitingCrushAfterUserResponds(t *testing.T) {
	for _, tt := range []struct {
		name   string
		output string
		want   domain.ActivityState
	}{
		{name: "resumed active", output: "> Working!\n", want: domain.ActivityActive},
		{name: "resumed idle", output: "> Ready?\n", want: domain.ActivityIdle},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(500, 0).UTC()
			session := activeSession(now, domain.HarnessCrush)
			session.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now.Add(-time.Second)}
			session.UpdatedAt = now.Add(-time.Second)
			sink := &fakeSink{}
			observer := New(
				fakeSessions{rows: []domain.SessionRecord{session}},
				sink,
				&fakeRuntime{output: tt.output},
				fakeAgents{domain.HarnessCrush: crush.New()},
				Config{Clock: func() time.Time { return now }, Logger: testLogger()},
			)

			if err := observer.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(sink.signals) != 1 || sink.signals[0].State != tt.want {
				t.Fatalf("unexpected reconciliation: %+v", sink.signals)
			}
		})
	}
}

func TestPollPreservesClaudeWaitingInputWithoutContinuousCapability(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	session := activeSession(now, domain.HarnessClaudeCode)
	session.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now.Add(-time.Second)}
	session.UpdatedAt = now.Add(-time.Second)
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: claudeStuckActiveScreen}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{session}},
		sink,
		runtime,
		fakeAgents{domain.HarnessClaudeCode: claudecode.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 0 || len(sink.signals) != 0 {
		t.Fatalf("sticky Claude waiting state was sampled: output calls=%d signals=%+v", runtime.calls, sink.signals)
	}
}

func TestPollLeavesOtherHarnessesUntouched(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: "› prompt\nmodel · ~/project\n"}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{activeSession(now, domain.HarnessDroid)}},
		sink,
		runtime,
		fakeAgents{domain.HarnessDroid: droid.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 0 || len(sink.signals) != 0 {
		t.Fatalf("unaffected harness: output calls=%d signals=%+v", runtime.calls, sink.signals)
	}
}

// claudeStuckActiveScreen is the rendered Claude Code surface after a turn
// aborted without its Stop hook (expired login): idle composer holding an
// unsent draft, provider footer below, no active chrome. This is the screen a
// session stranded in durable "active" actually shows.
const claudeStuckActiveScreen = "⏺ Login expired · Please run /login\n" +
	"\n" +
	"✻ Worked for 0s\n" +
	"\n" +
	"────────────────────────────────────────────────\n" +
	"❯ so btw, the status isn't permanently stuck\n" +
	"────────────────────────────────────────────────\n" +
	"\n" +
	"  ⏵⏵ bypass permissions on (shift+tab to cycle) · PR #4090\n"

func TestPollReconcilesStaleClaudeCodeAfterAbortedTurn(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	session := activeSession(now, domain.HarnessClaudeCode)
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: claudeStuckActiveScreen}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{session}},
		sink,
		runtime,
		fakeAgents{domain.HarnessClaudeCode: claudecode.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 {
		t.Fatalf("signals = %d, want 1", len(sink.signals))
	}
	signal := sink.signals[0]
	if sink.id != session.ID || signal.State != domain.ActivityWaitingInput || signal.Event != "terminal-waiting-input" {
		t.Fatalf("unexpected reconciliation: id=%q signal=%+v", sink.id, signal)
	}
	if signal.ExpectedRevision == nil || *signal.ExpectedRevision != session.Revision || signal.LaunchID != "launch-1" {
		t.Fatalf("reconciliation fence = %+v, want revision=%d launch=launch-1", signal, session.Revision)
	}
}

func TestPollKeepsGenuineLongClaudeTurnActive(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: "✻ Computing… (3m 10s · ↓ 114 tokens)\n" +
		"────────────────────────────────────────────────\n" +
		"❯\n" +
		"────────────────────────────────────────────────\n" +
		"⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt · ← for agents\n"}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{activeSession(now, domain.HarnessClaudeCode)}},
		sink,
		runtime,
		fakeAgents{domain.HarnessClaudeCode: claudecode.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 0 {
		t.Fatalf("long active turn emitted reconciliation: %+v", sink.signals)
	}
}

func TestPollSkipsFreshActiveAndOutputFailures(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	fresh := activeSession(now, domain.HarnessCodex)
	fresh.Activity.LastActivityAt = now.Add(-time.Minute)
	runtime := &fakeRuntime{err: errors.New("capture failed")}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{fresh}},
		&fakeSink{},
		runtime,
		fakeAgents{domain.HarnessCodex: codex.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 0 {
		t.Fatalf("fresh session output calls = %d, want 0", runtime.calls)
	}

	stale := activeSession(now, domain.HarnessCodex)
	observer.sessions = fakeSessions{rows: []domain.SessionRecord{stale}}
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPollReturnsSessionListFailure(t *testing.T) {
	want := errors.New("list failed")
	observer := New(fakeSessions{err: want}, &fakeSink{}, &fakeRuntime{}, nil, Config{Logger: testLogger()})
	if err := observer.Poll(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

type renderedRuntime struct {
	fakeRuntime
	rendered    string
	renderedErr error
}

type renderedWaitingAgent struct{ ports.Agent }

type unrenderedWaitingAgent struct{ renderedWaitingAgent }

func (unrenderedWaitingAgent) RequiresRenderedActivity() bool { return false }

func (renderedWaitingAgent) DetectTerminalActivity(string) (domain.ActivityState, bool) {
	return domain.ActivityWaitingInput, true
}
func (renderedWaitingAgent) ContinuouslyDetectTerminalActivity() bool { return true }
func (renderedWaitingAgent) RequiresRenderedActivity() bool           { return true }

func TestRenderedModalResolutionKeepsBoundaryEvent(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	rec := activeSession(now, domain.HarnessHermes)
	rec.Activity.State = domain.ActivityBlocked
	sink := &fakeSink{}
	observer := New(fakeSessions{rows: []domain.SessionRecord{rec}}, sink, &renderedRuntime{}, fakeAgents{domain.HarnessHermes: renderedWaitingAgent{}}, Config{Clock: func() time.Time { return now }, Logger: testLogger()})
	if err := observer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 || sink.signals[0].Event != "permission-resolved" || sink.signals[0].State != domain.ActivityWaitingInput {
		t.Fatalf("blocked to waiting lost modal boundary: %+v", sink.signals)
	}
}

func (r *renderedRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return r.rendered, r.renderedErr
}
func TestRenderedAdapterNeverUsesOldRingComposer(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	rec := activeSession(now, domain.HarnessHermes)
	sink := &fakeSink{}
	runtime := &renderedRuntime{fakeRuntime: fakeRuntime{output: "❯ \n"}, renderedErr: errors.New("surface unavailable")}
	observer := New(fakeSessions{rows: []domain.SessionRecord{rec}}, sink, runtime, fakeAgents{domain.HarnessHermes: hermes.New()}, Config{Clock: func() time.Time { return now }, Logger: testLogger()})
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 0 || runtime.calls != 0 {
		t.Fatal("fell back to stale output ring")
	}
	runtime.renderedErr = nil
	runtime.rendered = "Confirm: Allow this command?\n[1] Allow once"
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 0 {
		t.Fatalf("quoted modal text emitted activity: %+v", sink.signals)
	}
	runtime.rendered += "\n⚠ ❯"
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 || sink.signals[0].State != domain.ActivityBlocked {
		t.Fatalf("signals=%+v", sink.signals)
	}
	rec.Activity.State = domain.ActivityBlocked
	observer.sessions = fakeSessions{rows: []domain.SessionRecord{rec}}
	runtime.rendered = "────────────────────────────────────────\n❯ \n────────────────────────────────────────"
	sink.signals = nil
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 || sink.signals[0].State != domain.ActivityIdle || sink.signals[0].Event != "permission-resolved" {
		t.Fatalf("blocked recovery=%+v", sink.signals)
	}
}

type renderedMemcodeRuntime struct {
	*fakeRuntime
	rendered    string
	styledCalls int
}

func (r *renderedMemcodeRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.styledCalls++
	return r.rendered, r.err
}

func TestMemcodeDecisionObservationBlocksAndResolvesOnlyOnCurrentChrome(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	card := "Do you want to proceed?\n❯ 1. Yes\n→  Ask memcode…   ·   $ = shell\nmemcode · model · ask"
	idle := "○ idle\n→  Ask memcode…   ·   $ = shell\nmemcode · model · ask"
	for _, tc := range []struct {
		name    string
		current domain.ActivityState
		output  string
		want    domain.ActivityState
		event   string
	}{{"approval", domain.ActivityActive, card, domain.ActivityBlocked, "terminal-blocked"}, {"resolved", domain.ActivityBlocked, idle, domain.ActivityIdle, "permission-resolved"}, {"still blocked", domain.ActivityBlocked, card, "", ""}, {"unknown cannot resolve", domain.ActivityBlocked, "loading", "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			session := activeSession(now, domain.HarnessMemcode)
			session.Activity.State = tc.current
			sink := &fakeSink{}
			observer := New(fakeSessions{rows: []domain.SessionRecord{session}}, sink, &renderedMemcodeRuntime{fakeRuntime: &fakeRuntime{output: "stale raw history"}, rendered: tc.output}, fakeAgents{domain.HarnessMemcode: memcode.New()}, Config{Clock: func() time.Time { return now }, Logger: testLogger()})
			if err := observer.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(sink.signals) != 0 {
					t.Fatal(sink.signals)
				}
				return
			}
			if len(sink.signals) != 1 || sink.signals[0].State != tc.want || sink.signals[0].Event != tc.event || sink.signals[0].LaunchID != "launch-1" {
				t.Fatal(sink.signals)
			}
		})
	}
}

func TestMemcodeObservationUsesCurrentRenderedScreen(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	session := activeSession(now, domain.HarnessMemcode)
	sink := &fakeSink{}
	rt := &renderedMemcodeRuntime{fakeRuntime: &fakeRuntime{output: "⠋ Thinking… (1s · esc to interrupt)"}, rendered: "○ idle\n→  Ask memcode…   ·   $ = shell\nmemcode · model · ask"}
	observer := New(fakeSessions{rows: []domain.SessionRecord{session}}, sink, rt, fakeAgents{domain.HarnessMemcode: memcode.New()}, Config{Clock: func() time.Time { return now }, Logger: testLogger()})
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.calls != 0 || rt.styledCalls != 1 || len(sink.signals) != 1 || sink.signals[0].State != domain.ActivityIdle {
		t.Fatal(rt.calls, rt.styledCalls, sink.signals)
	}
	rawSink := &fakeSink{}
	observer = New(fakeSessions{rows: []domain.SessionRecord{session}}, rawSink, &fakeRuntime{output: rt.rendered}, fakeAgents{domain.HarnessMemcode: memcode.New()}, Config{Clock: func() time.Time { return now }, Logger: testLogger()})
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rawSink.signals) != 1 || rawSink.signals[0].State != domain.ActivityIdle {
		t.Fatal("raw output fallback did not detect current chrome", rawSink.signals)
	}
}
