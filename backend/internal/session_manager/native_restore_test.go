package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	st.sessions[rec.ID] = rec
	rt.aliveByHandle = map[string]bool{"tmux-mer-1": true}
	err := m.reconcileLive(ctx, rec)
	if err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatal(err)
	}
	if rt.destroyed != 1 {
		t.Fatal("unverified runtime adopted")
	}
}
func TestInteractiveActualStartupHealthRefusesUnverifiedBootstrap(t *testing.T) {
	a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
	m, st, rt, _ := interactiveManager(t, false, a)
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionIDLaunchID = "previous"
	st.sessions[rec.ID] = rec
	rt.aliveByHandle = map[string]bool{"tmux-mer-1": true}
	if err := m.checkSessionHealth(ctx, rec); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatal(err)
	}
	if rt.destroyed != 1 || st.sessions[rec.ID].Activity.State != domain.ActivityExited {
		t.Fatal("startup adopted fresh runtime")
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
