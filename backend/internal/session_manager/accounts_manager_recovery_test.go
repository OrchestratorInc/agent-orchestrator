package sessionmanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type accountSwitchPublicationFailure struct {
	*lifecycle.Manager
	fail bool
}

func (l *accountSwitchPublicationFailure) MarkSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	if l.fail {
		return errors.New("publication unavailable")
	}
	return l.Manager.MarkSpawned(ctx, id, metadata)
}

func TestAccountsManagerSwitchRetrySurvivesRestartAtPublicationBoundaries(t *testing.T) {
	for _, boundary := range []string{"binding sync", "route preparation", "runtime creation", "metadata publication"} {
		t.Run(boundary, func(t *testing.T) {
			m, st, rt, agent, rec, cfg := accountSwitchFixture(t)
			rt.outputs = []string{ambiguousTerminalOutput}
			m.switchTargetStartWait = 30 * time.Millisecond
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
				t.Fatal(err)
			}
			first := waitAccountSwitch(t, m, st, cfg.OperationID)
			if first.Phase != domain.AccountsManagerSwitchRecoveryRequired {
				t.Fatal("first target unexpectedly ready")
			}
			router := m.accountsManager.(*accountSwitchRouter)
			switch boundary {
			case "binding sync":
				router.syncHook = func(ctx context.Context) error {
					op, _, err := st.GetAccountsManagerSwitch(ctx, cfg.OperationID)
					if err != nil {
						return err
					}
					if op.TargetGeneration != first.TargetGeneration {
						return errors.New("post-rotation synchronization unavailable")
					}
					return nil
				}
			case "route preparation":
				router.routeErr = errors.New("route preparation unavailable")
			case "runtime creation":
				rt.createErr = errors.New("runtime creation unavailable")
			case "metadata publication":
				m.lcm = &accountSwitchPublicationFailure{Manager: lifecycle.New(st, nil), fail: true}
			}
			if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
				t.Fatal(err)
			}
			failed := waitAccountSwitch(t, m, st, cfg.OperationID)
			if failed.Phase != domain.AccountsManagerSwitchRecoveryRequired || failed.TargetGeneration == first.TargetGeneration || failed.RetiredTargetGeneration != first.TargetGeneration {
				t.Fatal("failed retry did not retain both controller identities")
			}
			project, err := m.loadProject(t.Context(), rec.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.OpenPreMigrated(project.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			rt.createErr, rt.outputs = nil, []string{idleTerminalOutput}
			restarted := New(Deps{Store: reopened, Runtime: accountSwitchRuntime{rt}, Lifecycle: lifecycle.New(reopened, nil),
				AccountsManager: &accountSwitchRouter{store: reopened}, Agents: singleAgent{agent: agent}, DataDir: t.TempDir(),
				LookPath: func(string) (string, error) { return "/bin/true", nil }, BackgroundContext: t.Context()})
			restarted.switchTargetStartWait = time.Second
			if err := restarted.reconcileAccountsManagerSwitches(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
				t.Fatal(err)
			}
			ready := waitAccountSwitch(t, restarted, reopened, cfg.OperationID)
			if ready.Phase != domain.AccountsManagerSwitchReady || ready.TargetRevision <= failed.TargetRevision || ready.TargetGeneration == failed.TargetGeneration {
				t.Fatalf("cold retry stranded: phase=%s code=%s", ready.Phase, ready.ErrorCode)
			}
			if agent.lastLaunch.Prompt != "" {
				t.Fatal("cold retry replayed task prompt")
			}
		})
	}
}

func TestAccountsManagerSwitchConfirmsLastTmuxStopOnRepeatedRecovery(t *testing.T) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "account-stop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("SHELL", "/bin/sh")
	runtime := tmux.New(tmux.Options{Binary: binary, LegacyBinary: binary, SocketName: "account-recovery", Timeout: time.Second})
	ref := ports.FencedRuntimeRef{SessionID: "last-account-session", Handle: ports.RuntimeHandle{ID: "last-account-session"}, Generation: "source-generation"}
	t.Cleanup(func() { _ = runtime.Destroy(context.Background(), ref.Handle) })
	if _, err := runtime.Create(t.Context(), ports.RuntimeConfig{SessionID: ref.SessionID, WorkspacePath: t.TempDir(), Argv: []string{"sleep", "30"}}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Destroy(t.Context(), ref.Handle); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Runtime: runtime})
	for range 3 {
		if err := m.stopAccountsManagerRuntime(t.Context(), ref); err != nil {
			t.Fatalf("confirmed server shutdown became an unknown stop: %v", err)
		}
	}
}

func TestAccountsManagerSwitchMissingHistoryNeverOverridesPriorWork(t *testing.T) {
	m, st, rt, _, rec, cfg := accountSwitchFixture(t)
	m.agents = singleAgent{agent: untouchedEmptyTransitionAgent{}}
	rec.Metadata.AgentSessionID, rec.Metadata.AgentSessionIDLaunchID = "reserved-without-history", rec.Metadata.RuntimeLaunchID
	rec.Metadata.LatestUserPromptAt = time.Now()
	if err := st.UpdateSession(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	cfg.NewConversation = false
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	op := waitAccountSwitch(t, m, st, cfg.OperationID)
	if op.Phase != domain.AccountsManagerSwitchFailed || rt.destroyed != 0 || rt.created != 0 || op.EmptySource {
		t.Fatal("missing history discarded prior conversation work")
	}
}

func TestAccountsManagerSwitchEmptySourceDecisionSurvivesColdRetry(t *testing.T) {
	m, st, rt, agent, rec, cfg := accountSwitchFixture(t)
	m.agents = singleAgent{agent: untouchedEmptyTransitionAgent{}}
	rec.Metadata.RuntimeHandleID = string(rec.ID)
	rec.Metadata.AgentSessionID, rec.Metadata.AgentSessionIDLaunchID = "reserved-without-history", rec.Metadata.RuntimeLaunchID
	if err := st.UpdateSession(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	rt.aliveByHandle = map[string]bool{string(rec.ID): true}
	rt.createErr = errors.New("first target unavailable")
	cfg.NewConversation = false
	rt.onDestroy = func(_ int, _ ports.RuntimeHandle) {
		op, _, err := st.GetAccountsManagerSwitch(t.Context(), cfg.OperationID)
		if err != nil || !op.EmptySource || op.NewConversation {
			t.Error("empty-history decision was not durable before stopping")
		}
	}
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	failed := waitAccountSwitch(t, m, st, cfg.OperationID)
	if !failed.EmptySource || failed.NewConversation || failed.TargetRevision == 0 {
		t.Fatal("empty-source launch decision was lost on failure")
	}
	project, err := m.loadProject(t.Context(), rec.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenPreMigrated(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rt.createErr, rt.onDestroy = nil, nil
	restarted := New(Deps{Store: reopened, Runtime: accountSwitchRuntime{rt}, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: &accountSwitchRouter{store: reopened},
		Agents: singleAgent{agent: agent}, DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil }, BackgroundContext: t.Context()})
	restarted.switchTargetStartWait = time.Second
	if err := restarted.reconcileAccountsManagerSwitches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	ready := waitAccountSwitch(t, restarted, reopened, cfg.OperationID)
	if ready.Phase != domain.AccountsManagerSwitchReady || !ready.EmptySource {
		t.Fatalf("cold empty-source retry stranded: phase=%s code=%s", ready.Phase, ready.ErrorCode)
	}
	if agent.lastLaunch.Prompt != "" {
		t.Fatal("cold empty-source retry replayed the saved task")
	}
}

type accountSwitchRotatingAcknowledgement struct {
	*sqlite.Store
	before func(context.Context, string) error
}

func (s accountSwitchRotatingAcknowledgement) AcknowledgeAccountsManagerSwitch(ctx context.Context, id, generation string) (domain.AccountsManagerSwitch, error) {
	if err := s.before(ctx, id); err != nil {
		return domain.AccountsManagerSwitch{}, err
	}
	return s.Store.AcknowledgeAccountsManagerSwitch(ctx, id, generation)
}

func TestAccountsManagerSwitchStaleReadinessCannotAcknowledgeNewGeneration(t *testing.T) {
	m, st, _, _, rec, cfg := accountSwitchFixture(t)
	m.store = accountSwitchRotatingAcknowledgement{Store: st, before: func(ctx context.Context, id string) error {
		op, _, err := st.GetAccountsManagerSwitch(ctx, id)
		if err != nil {
			return err
		}
		if _, err := st.AdvanceAccountsManagerSwitch(ctx, id, op.Phase, domain.AccountsManagerSwitchRecoveryRequired, "RESTART"); err != nil {
			return err
		}
		current, _, err := st.GetSession(ctx, rec.ID)
		if err != nil {
			return err
		}
		if _, err := st.RetryAccountsManagerSwitch(ctx, id, "new-unconfirmed-generation", current.ControllerOwner()); err != nil {
			return err
		}
		current.Metadata.RuntimeLaunchID = "new-unconfirmed-generation"
		if err := st.UpdateSession(ctx, current); err != nil {
			return err
		}
		_, err = st.AdvanceAccountsManagerSwitch(ctx, id, domain.AccountsManagerSwitchCommitted, domain.AccountsManagerSwitchStarting, "")
		return err
	}}
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	op := waitAccountSwitch(t, m, st, cfg.OperationID)
	if op.Phase == domain.AccountsManagerSwitchReady {
		t.Fatal("old readiness witness acknowledged the replacement generation")
	}
}
