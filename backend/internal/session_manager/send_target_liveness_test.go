package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func launchedTUISession(id domain.SessionID, launch string) domain.SessionRecord {
	return pastStartupGate(domain.SessionRecord{
		ID: id, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{RuntimeHandleID: string(id), RuntimeLaunchID: launch},
	})
}

func newTargetSendTest(t *testing.T, rec domain.SessionRecord, probe ports.FencedProbeResult) (*Manager, *fakeRuntime, *fakeMessenger, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	st.sessions[rec.ID] = rec
	msg := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, msg, st)
	rt := m.runtime.(*fakeRuntime)
	rt.fencedResult = probe
	return m, rt, msg, st
}

func TestSend_AliveTargetLaunchDelivers(t *testing.T) {
	rec := launchedTUISession("s1", "launch-a")
	m, rt, msg, _ := newTargetSendTest(t, rec, ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch})
	if err := m.Send(context.Background(), "s1", "hello", nil); err != nil {
		t.Fatalf("Send = %v", err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "hello") {
		t.Fatalf("writes = %q, want the one message", msg.msgs)
	}
	want := ports.FencedRuntimeRef{Handle: ports.RuntimeHandle{ID: "s1"}, SessionID: "s1", Generation: "launch-a"}
	if len(rt.fencedRefs) != 1 || rt.fencedRefs[0] != want {
		t.Fatalf("probe refs = %+v, want exactly %+v", rt.fencedRefs, want)
	}
}

func TestSend_ExitedTargetLaunchRejectsWithoutWriteOrDeathFact(t *testing.T) {
	rec := launchedTUISession("s1", "launch-a")
	m, _, msg, st := newTargetSendTest(t, rec, ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent})
	err := m.Send(context.Background(), "s1", "hello", nil)
	if !errors.Is(err, ErrAgentExited) {
		t.Fatalf("Send = %v, want ErrAgentExited", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("writes = %q, want none", msg.msgs)
	}
	if got := st.sessions["s1"]; got.Activity.State != domain.ActivityIdle || got.IsTerminated {
		t.Fatalf("send rejection changed lifecycle facts: %+v", got)
	}
}

// T05: a supported probe that fails is not a death verdict and not success.
func TestSend_InconclusiveTargetProbeRefusesWithoutDeathVerdict(t *testing.T) {
	for _, reason := range []ports.FencedProbeReason{
		ports.FencedReasonProbeFailed, ports.FencedReasonRegistryUnreadable, ports.FencedReasonRegistryMalformed,
	} {
		t.Run(string(reason), func(t *testing.T) {
			rec := launchedTUISession("s1", "launch-a")
			m, _, msg, st := newTargetSendTest(t, rec, ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: reason})
			err := m.Send(context.Background(), "s1", "hello", nil)
			if err == nil || errors.Is(err, ErrAgentExited) || !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
				t.Fatalf("Send = %v, want an inconclusive-probe error that is not AGENT_EXITED", err)
			}
			if !strings.Contains(err.Error(), string(reason)) {
				t.Fatalf("error %q does not name the probe reason", err)
			}
			if len(msg.msgs) != 0 {
				t.Fatalf("writes = %q, want none", msg.msgs)
			}
			if got := st.sessions["s1"]; got.Activity.State != domain.ActivityIdle || got.IsTerminated {
				t.Fatalf("unknown probe changed lifecycle facts: %+v", got)
			}
		})
	}
}

type blockingProbeRuntime struct {
	*fakeRuntime
	entered chan ports.FencedRuntimeRef
	release chan ports.FencedProbeResult
}

func (r *blockingProbeRuntime) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	r.entered <- ref
	select {
	case result := <-r.release:
		return result
	case <-ctx.Done():
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
}

func newBlockingProbeManager(t *testing.T, st *fakeStore, msg *fakeMessenger) (*Manager, *blockingProbeRuntime) {
	t.Helper()
	rt := &blockingProbeRuntime{fakeRuntime: &fakeRuntime{}, entered: make(chan ports.FencedRuntimeRef, 4), release: make(chan ports.FencedProbeResult, 4)}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{fakeAgent{}}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: msg, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	return m, rt
}

// T05: a probe that never answers is bounded by the caller's context and
// never reported as delivery or as exit.
func TestSend_HungTargetProbeIsBoundedAndRefused(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = launchedTUISession("s1", "launch-a")
	msg := &fakeMessenger{}
	m, _ := newBlockingProbeManager(t, st, msg)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := m.Send(ctx, "s1", "hello", nil)
	if err == nil || errors.Is(err, ErrAgentExited) || !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("Send = %v, want an inconclusive-probe error", err)
	}
	if elapsed := time.Since(started); elapsed > sendTargetProbeTimeout {
		t.Fatalf("probe was not bounded: %v", elapsed)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("writes = %q, want none", msg.msgs)
	}
}

// T06 + R04: a send admitted for launch A holds the input lease across its
// probe. A resume to launch B waits for that lease; A's exit verdict neither
// writes nor marks anything, and B then receives only its own message.
func TestSend_LaunchAProbeCannotAffectOrRedirectToLaunchB(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = launchedTUISession("s1", "launch-a")
	msg := &fakeMessenger{}
	m, rt := newBlockingProbeManager(t, st, msg)

	sendA := make(chan error, 1)
	go func() { sendA <- m.Send(context.Background(), "s1", "message for A", nil) }()
	if ref := <-rt.entered; ref.Generation != "launch-a" {
		t.Fatalf("probe generation = %q, want launch-a", ref.Generation)
	}

	resumed := make(chan error, 1)
	go func() { resumed <- m.beginAgentOperation(context.Background(), "s1", agentOperationResume) }()
	select {
	case err := <-resumed:
		t.Fatalf("resume admitted while launch A's send held the input lease: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	rt.release <- ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	if err := <-sendA; !errors.Is(err, ErrAgentExited) {
		t.Fatalf("send A = %v, want ErrAgentExited", err)
	}
	if err := <-resumed; err != nil {
		t.Fatalf("resume did not proceed after the lease drained: %v", err)
	}
	if got := st.sessions["s1"]; got.Activity.State != domain.ActivityIdle || got.IsTerminated {
		t.Fatalf("launch A's verdict changed lifecycle facts: %+v", got)
	}
	// Input stays gated until the resume commits B.
	if err := m.Send(context.Background(), "s1", "gated", nil); !errors.Is(err, ErrSwitchInProgress) {
		t.Fatalf("send during resume = %v, want ErrSwitchInProgress", err)
	}
	b := st.sessions["s1"]
	b.Metadata.RuntimeLaunchID = "launch-b"
	st.sessions["s1"] = b
	m.endAgentOperation("s1", agentOperationResume)

	sendB := make(chan error, 1)
	go func() { sendB <- m.Send(context.Background(), "s1", "message for B", nil) }()
	if ref := <-rt.entered; ref.Generation != "launch-b" {
		t.Fatalf("probe generation = %q, want launch-b", ref.Generation)
	}
	rt.release <- ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
	if err := <-sendB; err != nil {
		t.Fatalf("send B = %v", err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "message for B") {
		t.Fatalf("writes = %q, want only launch B's message", msg.msgs)
	}
}

// T06: the runtime owning another generation is a mismatch, not an exit.
func TestSend_GenerationMismatchRefusesWithoutExitVerdict(t *testing.T) {
	rec := launchedTUISession("s1", "launch-a")
	m, _, msg, st := newTargetSendTest(t, rec, ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonGenerationMismatch})
	err := m.Send(context.Background(), "s1", "hello", nil)
	if err == nil || errors.Is(err, ErrAgentExited) {
		t.Fatalf("Send = %v, want a non-exit refusal", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("writes = %q, want none", msg.msgs)
	}
	if got := st.sessions["s1"]; got.Activity.State != domain.ActivityIdle || got.IsTerminated {
		t.Fatalf("mismatch changed lifecycle facts: %+v", got)
	}
}

// R06: launches without a generation, and runtimes that cannot identify one
// (preserved legacy shells, pre-generation hosts), keep the unchecked path.
func TestSend_LegacyAndUnidentifiedLaunchesKeepDelivering(t *testing.T) {
	t.Run("no launch generation", func(t *testing.T) {
		rec := launchedTUISession("s1", "")
		m, rt, msg, _ := newTargetSendTest(t, rec, ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent})
		if err := m.Send(context.Background(), "s1", "hello", nil); err != nil {
			t.Fatalf("Send = %v", err)
		}
		if len(rt.fencedRefs) != 0 || len(msg.msgs) != 1 {
			t.Fatalf("probes=%d writes=%d, want 0 and 1", len(rt.fencedRefs), len(msg.msgs))
		}
	})
	t.Run("identity missing", func(t *testing.T) {
		rec := launchedTUISession("s1", "launch-a")
		m, _, msg, _ := newTargetSendTest(t, rec, ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonIdentityMissing})
		if err := m.Send(context.Background(), "s1", "hello", nil); err != nil {
			t.Fatalf("Send = %v", err)
		}
		if len(msg.msgs) != 1 {
			t.Fatalf("writes = %d, want 1", len(msg.msgs))
		}
	})
}

// R03/R08: the guard's existing refusals win before any runtime probe, so
// their error semantics and the no-Enter rule are unchanged.
func TestSend_ExistingRefusalsPrecedeTargetProbe(t *testing.T) {
	cases := []struct {
		name string
		edit func(*domain.SessionRecord)
		want error
	}{
		{"blocked", func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityBlocked }, ErrAwaitingDecision},
		{"durably exited", func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited }, ErrAgentExited},
		{"terminated", func(r *domain.SessionRecord) { r.IsTerminated = true }, ErrTerminated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := launchedTUISession("s1", "launch-a")
			tc.edit(&rec)
			m, rt, msg, _ := newTargetSendTest(t, rec, ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch})
			if err := m.Send(context.Background(), "s1", "hello", nil); !errors.Is(err, tc.want) {
				t.Fatalf("Send = %v, want %v", err, tc.want)
			}
			if len(rt.fencedRefs) != 0 || len(msg.msgs) != 0 {
				t.Fatalf("probes=%d writes=%q, want none", len(rt.fencedRefs), msg.msgs)
			}
		})
	}
	t.Run("missing session", func(t *testing.T) {
		m, rt, msg, _ := newTargetSendTest(t, launchedTUISession("other", "launch-a"), ports.FencedProbeResult{Liveness: ports.FencedDead})
		if err := m.Send(context.Background(), "s1", "hello", nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Send = %v, want ErrNotFound", err)
		}
		if len(rt.fencedRefs) != 0 || len(msg.msgs) != 0 {
			t.Fatalf("probes=%d writes=%q, want none", len(rt.fencedRefs), msg.msgs)
		}
	})
	t.Run("startup pending", func(t *testing.T) {
		st := newFakeStore()
		rec := launchedTUISession("s1", "launch-a")
		rec.Harness = domain.HarnessCursor
		rec.FirstSignalAt = time.Time{}
		st.sessions["s1"] = rec
		msg := &fakeMessenger{}
		m := newSendTestManager(t, startupReadySignalingAgent{}, msg, st)
		rt := m.runtime.(*fakeRuntime)
		if err := m.Send(context.Background(), "s1", "hello", nil); !errors.Is(err, ErrStartupPending) {
			t.Fatalf("Send = %v, want ErrStartupPending", err)
		}
		if len(rt.fencedRefs) != 0 || len(msg.msgs) != 0 {
			t.Fatalf("probes=%d writes=%q, want none", len(rt.fencedRefs), msg.msgs)
		}
	})
}

// R07: a caller that has already given up gets no write and no exit verdict;
// bounded completion after a write starts remains the runtime's contract.
func TestSend_CancelledCallerWritesNothingAndIsNotExit(t *testing.T) {
	rec := launchedTUISession("s1", "launch-a")
	st := newFakeStore()
	st.sessions["s1"] = rec
	msg := &fakeMessenger{}
	m, rt := newBlockingProbeManager(t, st, msg)
	ctx, cancel := context.WithCancel(context.Background())
	sent := make(chan error, 1)
	go func() { sent <- m.Send(ctx, "s1", "hello", nil) }()
	<-rt.entered
	cancel()
	err := <-sent
	if err == nil || errors.Is(err, ErrAgentExited) {
		t.Fatalf("Send = %v, want a non-exit refusal", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("writes = %q, want none", msg.msgs)
	}
}

// R05: Chat has no pane and never reaches the runtime probe.
func TestSend_ChatSkipsTerminalTargetProbe(t *testing.T) {
	rec := launchedTUISession("s1", "launch-a")
	rec.Mode = domain.SessionModeChat
	m, rt, msg, _ := newTargetSendTest(t, rec, ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent})
	chat := &recordingLauncher{}
	m.chat = chat
	if err := m.Send(context.Background(), "s1", "hello", nil); err != nil {
		t.Fatalf("Send = %v", err)
	}
	if len(rt.fencedRefs) != 0 || len(msg.msgs) != 0 || len(chat.relayed) != 1 {
		t.Fatalf("probes=%d pane writes=%d chat sends=%d, want 0/0/1", len(rt.fencedRefs), len(msg.msgs), len(chat.relayed))
	}
}

// R05: a lifecycle message held in the transition outbox and returned to a
// TUI source whose agent has exited stays pending instead of being marked
// delivered, so a later live controller can still receive it.
func TestTransitionOutboxRetainsMessageForExitedTUITarget(t *testing.T) {
	manager, store, _, chat, _ := newTransitionManager(t, domain.SessionModeTUI)
	chat.preflightErr = ports.ErrChatDriverUnavailable
	chat.preflightStarted = make(chan struct{}, 1)
	chat.preflightRelease = make(chan struct{})
	transition, err := manager.StartInterfaceTransition(
		context.Background(), "session-1", domain.SessionModeChat,
		domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	<-chat.preflightStarted
	if err := manager.Send(context.Background(), "session-1", "CI failed on linux", nil); err != nil {
		t.Fatalf("queued send = %v", err)
	}
	close(chat.preflightRelease)
	if settled := awaitTransition(t, store, transition.ID); settled.Phase != domain.SessionInterfaceTransitionFailed {
		t.Fatalf("phase = %q, want failed", settled.Phase)
	}
	// The seeded source runtime is not alive in this fixture: its launch exited.
	if err := manager.deliverAllTransitionMessages(context.Background()); !errors.Is(err, ErrAgentExited) {
		t.Fatalf("outbox delivery = %v, want ErrAgentExited", err)
	}
	pending, err := store.ListPendingSessionInterfaceTransitionMessages(context.Background(), transition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || len(store.messenger.msgs) != 0 {
		t.Fatalf("pending=%d pane writes=%d, want the message retained and unwritten", len(pending), len(store.messenger.msgs))
	}
}

// T05: with no caller deadline, the manager's own bound still ends a probe
// that never answers, so a held input lease cannot block mutations forever.
func TestSend_HungTargetProbeIsBoundedWithoutCallerDeadline(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = launchedTUISession("s1", "launch-a")
	msg := &fakeMessenger{}
	m, rt := newBlockingProbeManager(t, st, msg)
	sent := make(chan error, 1)
	started := time.Now()
	go func() { sent <- m.Send(context.Background(), "s1", "hello", nil) }()
	<-rt.entered
	select {
	case err := <-sent:
		elapsed := time.Since(started)
		if elapsed < sendTargetProbeTimeout {
			t.Fatalf("probe ended after %v, before the %v bound", elapsed, sendTargetProbeTimeout)
		}
		if err == nil || errors.Is(err, ErrAgentExited) || !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
			t.Fatalf("Send = %v, want an inconclusive-probe error", err)
		}
	case <-time.After(sendTargetProbeTimeout + 5*time.Second):
		t.Fatal("background send was not bounded by the manager's probe timeout")
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("writes = %q, want none", msg.msgs)
	}
}
