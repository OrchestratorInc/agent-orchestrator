package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const providerPromptTerminalOutput = "provider-prompt"

type composerProbeAgent struct{ transitionSurfaceAgent }

func (a composerProbeAgent) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	if output == providerPromptTerminalOutput {
		return ports.TerminalSurfaceObservation{Work: ports.TerminalSurfaceWorkWaitingInput, Composer: ports.TerminalComposerDraft}
	}
	return a.transitionSurfaceAgent.InspectTerminalSurface(output)
}

var fastComposerProbe = interfaceTransitionConfig{
	pollInterval:   time.Millisecond,
	idleSettle:     time.Nanosecond,
	staleIdleLimit: time.Second,
}

func draftProbeRecord(state domain.ActivityState) domain.SessionRecord {
	return domain.SessionRecord{
		ID:       "s1",
		Mode:     domain.SessionModeTUI,
		Harness:  "claude-code",
		Activity: domain.Activity{State: state, LastActivityAt: time.Now()},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"},
	}
}

func draftProbeManager(agent ports.Agent, frames ...string) (*Manager, *transitionRuntime, *bytes.Buffer) {
	rt := &transitionRuntime{
		fakeRuntime: &fakeRuntime{},
		outputForCall: func(call int) string {
			if call > len(frames) {
				return frames[len(frames)-1]
			}
			return frames[call-1]
		},
	}
	logs := &bytes.Buffer{}
	return &Manager{
		agents:              singleAgent{agent: agent},
		runtime:             rt,
		interfaceTransition: fastComposerProbe,
		logger:              slog.New(slog.NewTextHandler(logs, nil)),
	}, rt, logs
}

func TestComposerHoldsDraft(t *testing.T) {
	ctx := context.Background()
	noInput := time.Time{}

	t.Run("stable draft across samples is refused", func(t *testing.T) {
		m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
		if !m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityIdle), noInput) {
			t.Fatal("expected a stable draft to be detected")
		}
		if rt.styledOutputCalls != sendComposerDraftSamples {
			t.Fatalf("captures = %d, want %d for a confirmed draft", rt.styledOutputCalls, sendComposerDraftSamples)
		}
	})

	t.Run("draft is refused even when the agent acted after the last keystroke", func(t *testing.T) {
		m, _, _ := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
		rec := draftProbeRecord(domain.ActivityIdle)
		lastInputAt := rec.Activity.LastActivityAt.Add(-time.Minute)
		if !m.composerHoldsDraft(ctx, rec, lastInputAt) {
			t.Fatal("recency must not skip the probe when the screen can be read")
		}
	})

	t.Run("provider decision screen is not a draft", func(t *testing.T) {
		m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, decisionTerminalOutput)
		if m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityIdle), noInput) {
			t.Fatal("a blocked provider screen must not refuse sends")
		}
		if rt.styledOutputCalls != 1 {
			t.Fatalf("captures = %d, want 1", rt.styledOutputCalls)
		}
	})
	t.Run("provider input request is not a draft", func(t *testing.T) {
		m, _, _ := draftProbeManager(composerProbeAgent{}, providerPromptTerminalOutput)
		if m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityWaitingInput), noInput) {
			t.Fatal("a provider input request must not refuse sends")
		}
	})

	t.Run("waiting_input session with a real draft is refused", func(t *testing.T) {
		m, _, _ := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
		if !m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityWaitingInput), noInput) {
			t.Fatal("a draft at a waiting_input prompt must be protected")
		}
	})

	t.Run("draft that clears mid-sequence delivers", func(t *testing.T) {
		m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput, idleTerminalOutput)
		if m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityIdle), noInput) {
			t.Fatal("a draft must persist across every sample before refusing")
		}
		if rt.styledOutputCalls != 2 {
			t.Fatalf("captures = %d, want 2 (stop at the first empty frame)", rt.styledOutputCalls)
		}
	})

	t.Run("empty composer delivers on the first capture", func(t *testing.T) {
		m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, idleTerminalOutput)
		if m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityIdle), noInput) {
			t.Fatal("empty composer must not be treated as a draft")
		}
		if rt.styledOutputCalls != 1 {
			t.Fatalf("captures = %d, want 1", rt.styledOutputCalls)
		}
	})

	t.Run("waits for the last keystroke to settle before sampling", func(t *testing.T) {
		m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, idleTerminalOutput)
		m.interfaceTransition.idleSettle = 40 * time.Millisecond
		lastInputAt := time.Now()
		m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityIdle), lastInputAt)
		if len(rt.outputCallTimes) == 0 || rt.outputCallTimes[0].Before(lastInputAt.Add(m.interfaceTransition.idleSettle)) {
			t.Fatalf("sampled before the settle window elapsed: %v", rt.outputCallTimes)
		}
	})

	t.Run("capture error fails open and is logged", func(t *testing.T) {
		m, rt, logs := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
		rt.styledOutputErr = errors.New("capture unavailable")
		if m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityIdle), noInput) {
			t.Fatal("a capture error must fail open (deliver), not refuse")
		}
		if !strings.Contains(logs.String(), "capture unavailable") {
			t.Fatalf("capture error was not logged: %q", logs.String())
		}
	})

	t.Run("a hung capture is bounded and fails open", func(t *testing.T) {
		m, rt, logs := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
		m.runtime = hangingStyledRuntime{rt}
		m.interfaceTransition.staleIdleLimit = 30 * time.Millisecond
		start := time.Now()
		if m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityIdle), noInput) {
			t.Fatal("a probe that cannot finish must fail open")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("probe ran %v past its %v limit", elapsed, m.interfaceTransition.staleIdleLimit)
		}
		if !strings.Contains(logs.String(), "deadline") {
			t.Fatalf("probe timeout was not logged: %q", logs.String())
		}
	})

	t.Run("cancelled context fails open", func(t *testing.T) {
		m, _, _ := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if m.composerHoldsDraft(cancelled, draftProbeRecord(domain.ActivityIdle), noInput) {
			t.Fatal("a cancelled probe must not refuse")
		}
	})

	t.Run("active turn with a draft is inspected and refused", func(t *testing.T) {
		m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
		if !m.composerHoldsDraft(ctx, draftProbeRecord(domain.ActivityActive), noInput) {
			t.Fatal("an active turn with a proven draft must refuse, not deliver")
		}
		if rt.styledOutputCalls == 0 {
			t.Fatal("active turn must be inspected on screen, not skipped")
		}
	})

	failOpen := []struct {
		name   string
		mutate func(*Manager, *domain.SessionRecord)
	}{
		{"non-TUI session", func(_ *Manager, rec *domain.SessionRecord) { rec.Mode = domain.SessionModeChat }},
		{"terminated turn", func(_ *Manager, rec *domain.SessionRecord) { rec.Activity.State = domain.ActivityExited }},
		{"no agent registry", func(m *Manager, _ *domain.SessionRecord) { m.agents = nil }},
		{"harness without a surface inspector", func(m *Manager, _ *domain.SessionRecord) { m.agents = singleAgent{agent: fakeAgent{}} }},
		{"runtime without styled capture", func(m *Manager, _ *domain.SessionRecord) {
			m.runtime = &unstyledTransitionRuntime{runtime: m.runtime.(*transitionRuntime)}
		}},
		{"no runtime handle", func(_ *Manager, rec *domain.SessionRecord) { rec.Metadata.RuntimeHandleID = "" }},
	}
	for _, tc := range failOpen {
		t.Run(tc.name+" fails open without capturing", func(t *testing.T) {
			m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
			rec := draftProbeRecord(domain.ActivityIdle)
			tc.mutate(m, &rec)
			if m.composerHoldsDraft(ctx, rec, noInput) {
				t.Fatal("an unprovable case must fail open (deliver)")
			}
			if rt.styledOutputCalls != 0 {
				t.Fatalf("captures = %d, want 0", rt.styledOutputCalls)
			}
		})
	}
}

type hangingStyledRuntime struct{ *transitionRuntime }

func (hangingStyledRuntime) GetStyledOutput(ctx context.Context, _ ports.RuntimeHandle, _ int) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

type recordingInputGate struct {
	lastInputAt time.Time
	drainCalls  int
}

func (g *recordingInputGate) BeginInputDrain(string) (time.Time, func()) {
	g.drainCalls++
	return g.lastInputAt, func() {}
}

func (g *recordingInputGate) LastInputAt(string) time.Time { return g.lastInputAt }

type drainOnlyInputGate struct{ drainCalls int }

func (g *drainOnlyInputGate) BeginInputDrain(string) (time.Time, func()) {
	g.drainCalls++
	return time.Now(), func() {}
}

func TestComposerBusyCheckNeverClosesTerminalInput(t *testing.T) {
	m, _, _ := draftProbeManager(transitionSurfaceAgent{}, idleTerminalOutput)
	gate := &recordingInputGate{lastInputAt: time.Now()}
	m.SetTerminalInputGate(gate)

	m.composerBusyCheck()(context.Background(), draftProbeRecord(domain.ActivityIdle))

	if gate.drainCalls != 0 {
		t.Fatalf("the send probe closed terminal input %d time(s)", gate.drainCalls)
	}

	drainOnly := &drainOnlyInputGate{}
	m.SetTerminalInputGate(drainOnly)
	m.composerBusyCheck()(context.Background(), draftProbeRecord(domain.ActivityIdle))
	if drainOnly.drainCalls != 0 {
		t.Fatalf("a gate without a read-only accessor was drained %d time(s)", drainOnly.drainCalls)
	}
}

func composerSendTestManager(t *testing.T, frame string) (*Manager, *fakeMessenger) {
	t.Helper()
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"},
	})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, transitionSurfaceAgent{}, msg, st)
	m.runtime = &transitionRuntime{fakeRuntime: &fakeRuntime{}, outputForCall: func(int) string { return frame }}
	m.interfaceTransition = fastComposerProbe
	return m, msg
}

func TestSend_TUIComposerDraftRejectsDelivery(t *testing.T) {
	m, msg := composerSendTestManager(t, draftTerminalOutput)
	err := m.Send(context.Background(), "s1", "run the tests", nil)
	if !errors.Is(err, ErrComposerBusy) {
		t.Fatalf("Send error = %v, want ErrComposerBusy", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("Send wrote %d message(s) onto an unsent draft", len(msg.msgs))
	}
}

func TestSend_TUIEmptyComposerDelivers(t *testing.T) {
	m, msg := composerSendTestManager(t, idleTerminalOutput)
	if err := m.Send(context.Background(), "s1", "run the tests", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("Send calls = %d, want 1", len(msg.msgs))
	}
}

func TestSend_EnterOnlyResubmitSkipsTheComposerCheck(t *testing.T) {
	m, msg := composerSendTestManager(t, draftTerminalOutput)
	if err := m.Send(context.Background(), "s1", "", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("Send calls = %d, want the Enter-only resubmit delivered", len(msg.msgs))
	}
}

func TestSend_InternalReportDeliveryHonorsTheComposerCheck(t *testing.T) {
	m, msg := composerSendTestManager(t, draftTerminalOutput)
	report := domain.WrapReportDelivery("cm-1", "worker finished")
	err := m.Send(context.Background(), "s1", report, nil)
	if !errors.Is(err, ErrComposerBusy) {
		t.Fatalf("Send error = %v, want ErrComposerBusy (a report must not land on a human draft)", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("Send calls = %d, want 0 (refused before the write)", len(msg.msgs))
	}
}

type fencingInputGate struct {
	held     int
	released int
}

func (g *fencingInputGate) BeginInputDrain(string) (time.Time, func()) {
	return time.Time{}, func() {}
}
func (g *fencingInputGate) LastInputAt(string) time.Time { return time.Time{} }
func (g *fencingInputGate) HoldInput(string) (time.Time, func()) {
	g.held++
	return time.Time{}, func() { g.released++ }
}

type lateKeystrokeGate struct {
	renderDelay time.Duration
	mu          sync.Mutex
	keyAt       time.Time
}

func (g *lateKeystrokeGate) BeginInputDrain(string) (time.Time, func()) {
	return time.Time{}, func() {}
}
func (g *lateKeystrokeGate) LastInputAt(string) time.Time { return time.Time{} }
func (g *lateKeystrokeGate) HoldInput(string) (time.Time, func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.keyAt = time.Now()
	return g.keyAt, func() {}
}

func (g *lateKeystrokeGate) frame(int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.keyAt.IsZero() && time.Since(g.keyAt) >= g.renderDelay {
		return draftTerminalOutput
	}
	return idleTerminalOutput
}

func TestComposerBusyCheck_FencedRecheckWaitsForAKeystrokeToRender(t *testing.T) {
	gate := &lateKeystrokeGate{renderDelay: 30 * time.Millisecond}
	m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, idleTerminalOutput)
	m.interfaceTransition.idleSettle = 80 * time.Millisecond
	rt.outputForCall = gate.frame
	m.SetTerminalInputGate(gate)

	busy, release := m.composerBusyCheck()(context.Background(), draftProbeRecord(domain.ActivityIdle))
	if release != nil {
		release()
	}
	if !busy {
		t.Fatal("the fenced re-check captured before the newest keystroke rendered and delivered onto it")
	}
}

func TestComposerBusyCheck_FencedRecheckCatchesDraftAfterProbe(t *testing.T) {
	m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, idleTerminalOutput, draftTerminalOutput)
	gate := &fencingInputGate{}
	m.SetTerminalInputGate(gate)

	busy, release := m.composerBusyCheck()(context.Background(), draftProbeRecord(domain.ActivityIdle))
	if !busy {
		t.Fatal("a draft appearing after the probe must be caught by the fenced re-check")
	}
	if release != nil {
		t.Fatal("a refusal must not hand back a held fence")
	}
	if gate.held != 1 || gate.released != 1 {
		t.Fatalf("fence held=%d released=%d, want 1/1 (acquired then released on refusal)", gate.held, gate.released)
	}
	if rt.styledOutputCalls != 1+sendComposerDraftSamples {
		t.Fatalf("captures = %d, want an empty probe followed by %d fenced draft samples", rt.styledOutputCalls, sendComposerDraftSamples)
	}
}

func TestComposerBusyCheck_FenceHeldForDeliveryUntilRelease(t *testing.T) {
	m, _, _ := draftProbeManager(transitionSurfaceAgent{}, idleTerminalOutput)
	gate := &fencingInputGate{}
	m.SetTerminalInputGate(gate)

	busy, release := m.composerBusyCheck()(context.Background(), draftProbeRecord(domain.ActivityIdle))
	if busy {
		t.Fatal("an empty composer must deliver")
	}
	if release == nil {
		t.Fatal("delivery must hand back the held fence for the guard to release after the write")
	}
	if gate.held != 1 || gate.released != 0 {
		t.Fatalf("fence held=%d released=%d, want held once and not yet released before the write", gate.held, gate.released)
	}
	release()
	if gate.released != 1 {
		t.Fatalf("release did not free the fence: released=%d", gate.released)
	}
}

func TestComposerBusyCheck_FencedDraftRequiresConsecutiveSamples(t *testing.T) {
	m, rt, _ := draftProbeManager(transitionSurfaceAgent{}, idleTerminalOutput, draftTerminalOutput, idleTerminalOutput)
	m.SetTerminalInputGate(&fencingInputGate{})
	busy, release := m.composerBusyCheck()(context.Background(), draftProbeRecord(domain.ActivityIdle))
	if release != nil {
		defer release()
	}
	if busy {
		t.Fatalf("refused after %d captures despite only one draft frame and no accepted keystroke", rt.styledOutputCalls)
	}
}

type nudgeSafeSurfaceAgent struct{ transitionSurfaceAgent }

func (nudgeSafeSurfaceAgent) EmitsSubmitActivity() bool  { return true }
func (nudgeSafeSurfaceAgent) EmitsBlockedActivity() bool { return true }

func TestSend_NoEnterNudgeAfterTheOperatorTypes(t *testing.T) {
	m, msg := composerSendTestManager(t, idleTerminalOutput)
	m.agents = singleAgent{agent: nudgeSafeSurfaceAgent{}}
	gate := &recordingInputGate{}
	m.SetTerminalInputGate(gate)
	msg.onSend = func(_ domain.SessionID, message string) {
		if message != "" {
			gate.lastInputAt = time.Now()
		}
	}

	err := m.Send(context.Background(), "s1", "run the tests", nil)
	if !errors.Is(err, ErrDeliveryUnconfirmed) {
		t.Fatalf("Send error = %v, want ErrDeliveryUnconfirmed", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("Send calls = %d, want 1: Enter nudged into the operator's typing (%q)", len(msg.msgs), msg.msgs[1:])
	}
}

func TestSend_EnterNudgeStillRunsWhenNobodyTyped(t *testing.T) {
	m, msg := composerSendTestManager(t, idleTerminalOutput)
	m.agents = singleAgent{agent: nudgeSafeSurfaceAgent{}}
	m.SetTerminalInputGate(&recordingInputGate{})

	err := m.Send(context.Background(), "s1", "run the tests", nil)
	if !errors.Is(err, ErrDeliveryUnconfirmed) {
		t.Fatalf("Send error = %v, want ErrDeliveryUnconfirmed", err)
	}
	if want := m.sendConfirm.maxAttempts; len(msg.msgs) != want {
		t.Fatalf("Send calls = %d, want %d (the prompt plus its Enter nudges)", len(msg.msgs), want)
	}
}

type confirmationInputGate struct {
	recordingInputGate
	held bool
}

func (g *confirmationInputGate) HoldInput(string) (time.Time, func()) {
	g.held = true
	return g.lastInputAt, func() { g.held = false }
}

type confirmationInputLease struct {
	acquires int
	gate     *confirmationInputGate
}

func (l *confirmationInputLease) AcquireSessionInput(domain.SessionID) (func(), bool) {
	l.acquires++
	if l.acquires == 2 && !l.gate.held {
		l.gate.lastInputAt = time.Now()
	}
	return func() {}, true
}

func TestSend_ConfirmationEnterRechecksInputAfterAdmission(t *testing.T) {
	m, msg := composerSendTestManager(t, idleTerminalOutput)
	m.agents = singleAgent{agent: nudgeSafeSurfaceAgent{}}
	gate := &confirmationInputGate{}
	m.SetTerminalInputGate(gate)
	m.messenger.SetInputLease(&confirmationInputLease{gate: gate})

	err := m.Send(context.Background(), "s1", "run the tests", nil)
	if !errors.Is(err, ErrDeliveryUnconfirmed) {
		t.Fatalf("Send error = %v, want ErrDeliveryUnconfirmed", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("retry Enter reached human draft: got %d sends (%q), want only original delivery", len(msg.msgs), msg.msgs)
	}
}

func TestSend_ConfirmationEnterHoldsInputFence(t *testing.T) {
	m, msg := composerSendTestManager(t, idleTerminalOutput)
	m.agents = singleAgent{agent: nudgeSafeSurfaceAgent{}}
	gate := &confirmationInputGate{}
	m.SetTerminalInputGate(gate)
	msg.onSend = func(_ domain.SessionID, message string) {
		if message == "" && !gate.held {
			t.Error("confirmation Enter was written without holding the input fence")
		}
	}
	err := m.Send(context.Background(), "s1", "run the tests", nil)
	if !errors.Is(err, ErrDeliveryUnconfirmed) {
		t.Fatalf("Send error = %v, want ErrDeliveryUnconfirmed", err)
	}
	if len(msg.msgs) != m.sendConfirm.maxAttempts {
		t.Fatalf("Send calls = %d, want %d", len(msg.msgs), m.sendConfirm.maxAttempts)
	}
	if gate.held {
		t.Fatal("confirmation left the input fence held")
	}
}
