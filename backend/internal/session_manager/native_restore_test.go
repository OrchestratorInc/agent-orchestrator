package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/memcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

type interactiveTestAgent struct {
	fakeAgent
	plan        *interactiveTestPlan
	preflight   error
	directCalls int
}

func (a *interactiveTestAgent) PrepareNativeRestore(context.Context, ports.RestoreConfig) (ports.NativeRestoreInitialization, error) {
	if a.preflight != nil {
		return nil, a.preflight
	}
	return a.plan, a.preflight
}
func (a *interactiveTestAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	a.directCalls++
	return nil, false, errors.New("direct resume forbidden")
}

type interactiveTestPlan struct {
	ready    func(context.Context) (bool, error)
	restored func(context.Context) (bool, error)
}

func (*interactiveTestPlan) Argv() []string { return []string{"native"} }
func (*interactiveTestPlan) LaunchEnv() map[string]string {
	return map[string]string{"restore-test": "1"}
}
func (*interactiveTestPlan) ResumeInput() string { return "/resume agent-x" }
func (*interactiveTestPlan) TargetID() string    { return "agent-x" }
func (p *interactiveTestPlan) Ready(ctx context.Context, _ string, _ string) (bool, error) {
	if p.ready != nil {
		return p.ready(ctx)
	}
	return true, nil
}
func (p *interactiveTestPlan) Restored(ctx context.Context, _ string, _ string) (bool, error) {
	if p.restored != nil {
		return p.restored(ctx)
	}
	return true, nil
}

type nativeBindingLCM struct {
	*fakeLCM
	discard bool
}

func (l *nativeBindingLCM) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if l.discard {
		return nil
	}
	if err := l.fakeLCM.ApplyActivitySignal(ctx, id, s); err != nil {
		return err
	}
	r := l.store.sessions[id]
	if s.AgentSessionID != "" {
		r.Metadata.AgentSessionID = s.AgentSessionID
		r.Metadata.AgentSessionIDLaunchID = s.LaunchID
	}
	l.store.sessions[id] = r
	return nil
}

type renderedRestoreRuntime struct {
	*fakeRuntime
	calls int
	err   error
}

func (r *renderedRestoreRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.calls++
	return "CURRENT RENDERED SCREEN", r.err
}

func interactiveManager(t *testing.T, terminated bool, a *interactiveTestAgent) (*Manager, *fakeStore, *fakeRuntime, *fakeMessenger) {
	t.Helper()
	rt := &fakeRuntime{}
	m, st, _ := newExitedResumeManager(t, rt, a)
	m.runtime = &renderedRestoreRuntime{fakeRuntime: rt}
	r := st.sessions["mer-1"]
	r.IsTerminated = terminated
	r.Harness = domain.HarnessMemcode
	r.Metadata.AgentSessionIDLaunchID = "launch-old"
	st.sessions[r.ID] = r
	m.lcm = &nativeBindingLCM{fakeLCM: &fakeLCM{store: st}}
	msg := &fakeMessenger{}
	m.messenger = sessionguard.New(st, msg, m.logger)
	m.messenger.SetInputLease(m)
	return m, st, rt, msg
}

func TestMemcodeNativeLaunchUsesProcessSupervisor(t *testing.T) {
	m := New(Deps{
		DataDir:    "/ao-data",
		Executable: func() (string, error) { return "/opt/ao", nil },
	})
	env := map[string]string{}
	argv, err := m.wrapAgentProcessWithLaunchID(memcode.New(), "mer-1", env, []string{"memcode", "run"}, "launch-1", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/ao", "agent-process", "supervise", "--session", "mer-1", "--launch", "launch-1", "--", "memcode", "run"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("Memcode runtime argv = %#v, want %#v", argv, want)
	}
	if env[EnvSupervisedProcess] != "1" || env[EnvRuntimeLaunchID] != "launch-1" || env["AO_MEMCODE_GENERATION"] != "launch-1" {
		t.Fatalf("Memcode supervised launch env = %#v", env)
	}
}

func TestInteractiveNativeRestoreAndResume(t *testing.T) {
	for _, terminated := range []bool{true, false} {
		name := "resume"
		if terminated {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
			m, st, rt, msg := interactiveManager(t, terminated, a)
			a.plan.ready = func(context.Context) (bool, error) {
				if release, ok := m.AcquireSessionInput("mer-1"); ok {
					release()
					t.Fatal("public input admitted during initialization")
				}
				if st.sessions["mer-1"].Metadata.AgentSessionIDLaunchID == "launch-new" {
					t.Fatal("identity bound before verification")
				}
				return true, nil
			}
			var result RestoreResult
			var err error
			if terminated {
				result, err = m.RestoreWithMode(ctx, "mer-1")
			} else {
				result, err = m.ResumeAgentWithMode(ctx, "mer-1")
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Mode != RestoreModeNative || result.Session.Metadata.AgentSessionIDLaunchID != "launch-new" {
				t.Fatal(result)
			}
			if rt.outputCalls != 0 || m.runtime.(*renderedRestoreRuntime).calls < 3 {
				t.Fatal("restore used raw history instead of current viewport")
			}
			if a.directCalls != 0 || len(msg.msgs) != 1 || msg.msgs[0] != "/resume agent-x" {
				t.Fatal(a.directCalls, msg.msgs)
			}
			if strings.Contains(strings.Join(rt.lastCfg.Argv, " "), "continue the task") {
				t.Fatal("saved prompt replayed")
			}
			release, ok := m.AcquireSessionInput("mer-1")
			if !ok {
				t.Fatal("input admission not released")
			}
			release()
		})
	}
}

func TestInteractiveRestoreFallsBackWhenNativeHistoryNeverStarted(t *testing.T) {
	a := &interactiveTestAgent{preflight: ports.ErrNativeHistoryUnavailable}
	m, st, rt, _ := interactiveManager(t, true, a)
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionID = ""
	rec.Metadata.AgentSessionIDLaunchID = ""
	st.sessions[rec.ID] = rec
	result, err := m.RestoreWithMode(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode == RestoreModeNative || rt.created != 1 || a.directCalls != 0 {
		t.Fatalf("missing history did not launch fresh: mode=%q created=%d direct=%d", result.Mode, rt.created, a.directCalls)
	}
}

func TestInteractiveRestoreDoesNotResumeUnverifiedGeneration(t *testing.T) {
	a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
	m, st, rt, msg := interactiveManager(t, true, a)
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionIDLaunchID = "unverified-previous"
	st.sessions[rec.ID] = rec
	result, err := m.RestoreWithMode(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode == RestoreModeNative || rt.created != 1 || len(msg.msgs) != 0 {
		t.Fatalf("unverified generation resumed natively: mode=%q created=%d messages=%v", result.Mode, rt.created, msg.msgs)
	}
}
func TestInteractiveNativeFailureCleanupAndRetryState(t *testing.T) {
	for _, terminated := range []bool{true, false} {
		for _, failure := range []string{"history", "timeout", "exit", "wrong-id", "discarded-publication", "cleanup"} {
			t.Run(failure+map[bool]string{true: "-restore", false: "-resume"}[terminated], func(t *testing.T) {
				a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
				m, st, rt, msg := interactiveManager(t, terminated, a)
				callctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
				defer cancel()
				switch failure {
				case "history":
					a.preflight = errors.New("missing history")
				case "timeout":
					a.plan.ready = func(context.Context) (bool, error) { return false, nil }
				case "exit":
					dead := false
					rt.supervisedAliveOverride = &dead
				case "wrong-id":
					a.plan.restored = func(context.Context) (bool, error) { return false, errors.New("wrong native identity") }
				case "discarded-publication":
					m.lcm.(*nativeBindingLCM).discard = true
				case "cleanup":
					rt.destroyErr = errors.New("destroy failed")
					a.plan.restored = func(context.Context) (bool, error) { return false, errors.New("wrong native identity") }
				}
				var err error
				if terminated {
					_, err = m.RestoreWithMode(callctx, "mer-1")
				} else {
					_, err = m.ResumeAgentWithMode(callctx, "mer-1")
				}
				if err == nil {
					t.Fatal("failure accepted")
				}
				rec := st.sessions["mer-1"]
				if rec.Metadata.AgentSessionID != "agent-x" {
					t.Fatal("durable target overwritten")
				}
				if failure == "history" {
					if rt.created != 0 || len(msg.msgs) != 0 {
						t.Fatal("invalid history launched")
					}
				} else {
					if rt.destroyed != 1 {
						t.Fatal("new runtime not cleaned", rt.destroyed)
					}
					if failure != "cleanup" && rec.IsTerminated != terminated {
						t.Fatal("wrong rollback state", rec)
					}
					if failure == "cleanup" && !strings.Contains(err.Error(), "destroy failed") {
						t.Fatal("cleanup failure hidden", err)
					}
					if failure == "cleanup" {
						if rec.IsTerminated != terminated {
							t.Fatal("cleanup failure did not restore termination state", rec)
						}
						wantSendErr := ErrAgentExited
						if terminated {
							wantSendErr = ErrTerminated
						} else if rec.Activity.State != domain.ActivityExited {
							t.Fatal("cleanup failure did not quarantine agent input", rec)
						}
						before := len(msg.msgs)
						if sendErr := m.SendWithOptions(ctx, "mer-1", "must not reach unverified runtime", nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); !errors.Is(sendErr, wantSendErr) {
							t.Fatalf("send after cleanup failure = %v, want %v", sendErr, wantSendErr)
						}
						if len(msg.msgs) != before {
							t.Fatal("input reached unverified runtime after cleanup failure")
						}
					}
				}
				release, ok := m.AcquireSessionInput("mer-1")
				if !ok {
					t.Fatal("input admission leaked")
				}
				release()
			})
		}
	}
}
func TestInteractiveCrashReconcileRefusesUnverifiedBootstrap(t *testing.T) {
	a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
	m, st, rt, _ := interactiveManager(t, false, a)
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionIDLaunchID = "previous"
	rec.Metadata.RuntimeLaunchID = "launch-old"
	rec.Activity.State = domain.ActivityActive
	st.sessions[rec.ID] = rec
	rt.aliveByHandle = map[string]bool{"tmux-mer-1": true}
	if err := m.reconcileLive(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rt.destroyed != 0 || st.sessions[rec.ID].Activity.State == domain.ActivityExited {
		t.Fatal("healthy runtime destroyed for a missing start-hook identity")
	}
}
func TestInteractiveActualStartupHealthRefusesUnverifiedBootstrap(t *testing.T) {
	a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
	m, st, rt, _ := interactiveManager(t, false, a)
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionIDLaunchID = "previous"
	rec.Activity.State = domain.ActivityActive
	st.sessions[rec.ID] = rec
	rt.aliveByHandle = map[string]bool{"tmux-mer-1": true}
	if err := m.checkSessionHealth(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rt.destroyed != 0 || st.sessions[rec.ID].Activity.State == domain.ActivityExited {
		t.Fatal("startup destroyed healthy runtime")
	}
}
func TestNativeMemcodeDecisionRefusesOrdinarySend(t *testing.T) {
	a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
	m, st, _, msg := interactiveManager(t, false, a)
	r := st.sessions["mer-1"]
	r.Activity.State = domain.ActivityBlocked
	st.sessions[r.ID] = r
	err := m.SendWithOptions(ctx, r.ID, "another task", nil, ports.MessageDeliveryOptions{AuthoredByUser: true})
	if !errors.Is(err, ErrAwaitingDecision) || len(msg.msgs) != 0 {
		t.Fatal("pending decision received task input", err, msg.msgs)
	}
}

func TestInteractiveRestoreRequiresRenderedScreen(t *testing.T) {
	a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
	m, _, raw, _ := interactiveManager(t, true, a)
	m.runtime = &renderedRestoreRuntime{fakeRuntime: raw, err: errors.New("current rendered screen unavailable")}
	m.nativeRestoreProofWait = 50 * time.Millisecond
	if _, err := m.RestoreWithMode(ctx, "mer-1"); err == nil || !strings.Contains(err.Error(), "current rendered") {
		t.Fatal(err)
	}
	if raw.outputCalls != 0 {
		t.Fatal("raw output substituted for unavailable rendered screen")
	}
	if raw.destroyed != 1 {
		t.Fatal("unsupported runtime not rolled back", raw.destroyed)
	}
}

// A provider's current-generation receipt and composer are insufficient when
// its exact supervised child has exited or the process probe is inconclusive.
type receiptRestoreVerifier struct{}

func (receiptRestoreVerifier) VerifyNativeRestore(context.Context, ports.RestoreConfig, string, string) (bool, error) {
	return true, nil
}

func TestNativeRestoreReceiptRequiresLiveExactGeneration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		alive     bool
		probeErr  error
		wantError bool
	}{
		{name: "live exact child", alive: true},
		{name: "receipt survives dead child", wantError: true},
		{name: "probe unknown", probeErr: errors.New("probe unavailable"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &fakeRuntime{aliveByHandle: map[string]bool{"target": tc.alive}, supervisedErr: tc.probeErr}
			m := New(Deps{Runtime: &renderedRestoreRuntime{fakeRuntime: rt}})
			err := m.verifyNativeRestore(t.Context(), receiptRestoreVerifier{}, ports.RestoreConfig{
				Session: ports.SessionRef{ID: "mastra-1"},
			}, "replacement", ports.RuntimeHandle{ID: "target"})
			if (err != nil) != tc.wantError {
				t.Fatalf("verification error = %v, wantError=%t", err, tc.wantError)
			}
		})
	}
}

// flakyRenderedRuntime fails its first screen reads, then renders.
type flakyRenderedRuntime struct {
	*fakeRuntime
	failures int
}

func (r *flakyRenderedRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	if r.failures > 0 {
		r.failures--
		return "", errors.New("transient read failure")
	}
	return "CURRENT RENDERED SCREEN", nil
}

// A failed read of the replacement's screen is retried like an unready
// composer; only the proof deadline turns it into a failed restore.
func TestNativeRestoreRetriesTransientScreenReads(t *testing.T) {
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"target": true}}
	m := New(Deps{Runtime: &flakyRenderedRuntime{fakeRuntime: rt, failures: 2}})
	err := m.verifyNativeRestore(t.Context(), receiptRestoreVerifier{}, ports.RestoreConfig{
		Session: ports.SessionRef{ID: "mastra-1"},
	}, "replacement", ports.RuntimeHandle{ID: "target"})
	if err != nil {
		t.Fatalf("transient read failures failed the restore: %v", err)
	}
	m = New(Deps{Runtime: &flakyRenderedRuntime{fakeRuntime: rt, failures: 1 << 30}})
	m.nativeRestoreProofWait = 50 * time.Millisecond
	err = m.verifyNativeRestore(t.Context(), receiptRestoreVerifier{}, ports.RestoreConfig{
		Session: ports.SessionRef{ID: "mastra-1"},
	}, "replacement", ports.RuntimeHandle{ID: "target"})
	if err == nil || !strings.Contains(err.Error(), "transient read failure") {
		t.Fatalf("persistent read failure = %v; want it reported at the deadline", err)
	}
}

// failingProofAgent restores natively but its identity proof always fails.
type failingProofAgent struct{ *interactiveTestAgent }

func (failingProofAgent) VerifyNativeRestore(context.Context, ports.RestoreConfig, string, string) (bool, error) {
	return false, errors.New("resumed a different native thread")
}

// A stale TUI replacement that dies during the identity proof is fenced from
// the old row, like one that dies at the start check, so the failure must
// reconcile it as exited instead of leaving an idle row nothing can resume.
func TestFailedNativeIdentityReconcilesDeadStaleTUIReplacement(t *testing.T) {
	rt := &fakeRuntime{fencedResult: ports.FencedProbeResult{Liveness: ports.FencedDead}}
	agent := failingProofAgent{&interactiveTestAgent{plan: &interactiveTestPlan{}}}
	m, st, _ := newExitedResumeManager(t, rt, agent)
	m.runtime = &renderedRestoreRuntime{fakeRuntime: rt}
	m.lcm = &nativeBindingLCM{fakeLCM: &fakeLCM{store: st}}
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessMemcode
	rec.Activity.State = domain.ActivityIdle
	rec.Metadata.AgentSessionIDLaunchID = "launch-old"
	rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now()}
	st.sessions[rec.ID] = rec

	_, err := m.resumeAgentRecordWithPolicy(context.Background(), "version refresh", rec, true)
	if err == nil || !strings.Contains(err.Error(), "native identity") {
		t.Fatalf("restore error = %v; want the native identity failure", err)
	}
	got := st.sessions[rec.ID]
	if got.Activity.State != domain.ActivityExited {
		t.Fatalf("activity = %q; want the dead replacement reconciled as exited", got.Activity.State)
	}
	if got.Metadata.RuntimeLaunchID != "launch-old" || got.Metadata.AgentSessionID != "agent-x" || got.StaleBinary == nil {
		t.Fatalf("failed restore lost recoverable state: %+v", got)
	}
}
