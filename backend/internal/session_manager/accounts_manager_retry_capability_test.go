package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type accountRetryCapability interface {
	AccountsManagerSwitchCanRetry(domain.AccountsManagerSwitch) bool
}

func TestAccountsManagerSwitchRetryCapabilityAfterReopen(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting} {
			for _, action := range []string{"resume", "cancel"} {
				t.Run(prefix+"/"+string(phase)+"/"+action, func(t *testing.T) {
					m, st, rt, _, rec, cfg := accountSwitchFixture(t)
					rec.Metadata.RuntimeHandleID = prefix + string(rec.ID)
					if err := st.UpdateSession(t.Context(), rec); err != nil {
						t.Fatal(err)
					}
					rt.aliveByHandle = map[string]bool{rec.Metadata.RuntimeHandleID: true}
					provider, _ := accountsManagerProvider(rec.Harness)
					op, _, err := st.CreateAccountsManagerSwitch(t.Context(), domain.AccountsManagerSwitch{
						ID: cfg.OperationID, SessionID: rec.ID, Provider: provider, SourceMode: domain.AccountsManagerManaged,
						SourceAccountID: "account-a", SourceRevision: cfg.ExpectedRevision, SourceOwner: rec.ControllerOwner(),
						SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID, TargetMode: cfg.Mode, TargetAccountID: cfg.AccountID,
						TargetGeneration: "reserved", Policy: cfg.Policy, NewConversation: true,
					})
					if err != nil {
						t.Fatal(err)
					}
					if phase == domain.AccountsManagerSwitchWaiting {
						op, err = st.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, phase, "")
						if err != nil {
							t.Fatal(err)
						}
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
					boundary := &accountStopBoundaryStore{Store: reopened, before: true, entered: make(chan struct{}), release: make(chan struct{})}
					ctx, cancel := context.WithCancel(t.Context())
					restarted := New(Deps{Store: boundary, Runtime: m.runtime, Lifecycle: lifecycle.New(reopened, nil),
						AccountsManager: &accountSwitchRouter{store: reopened}, Agents: m.agents, DataDir: m.dataDir, LookPath: m.lookPath, BackgroundContext: ctx})
					restarted.switchTargetStartWait = time.Second
					restarted.interfaceTransition.pollInterval = time.Millisecond
					defer func() {
						cancel()
						joinCtx, joinCancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer joinCancel()
						if err := restarted.WaitAgentSwitchWorkers(joinCtx); err != nil {
							t.Error(err)
						}
					}()
					if restarted.AccountsManagerSwitchCanRetry(op) {
						t.Fatal("retry exposed before startup restored the reservation")
					}
					if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
						t.Fatal(err)
					}
					if !restarted.AccountsManagerSwitchCanRetry(op) {
						t.Fatal("recovered pre-stop operation has no retry action")
					}
					if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil {
						t.Fatal(err)
					}
					select {
					case <-boundary.entered:
					case <-time.After(3 * time.Second):
						t.Fatal("retry did not reach the pre-stop barrier")
					}
					if restarted.AccountsManagerSwitchCanRetry(op) {
						t.Fatal("running retry advertised a second retry")
					}
					if action == "cancel" {
						if _, err := restarted.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil {
							t.Fatal(err)
						}
					} else {
						close(boundary.release)
					}
					final := waitAccountSwitch(t, restarted, reopened, op.ID)
					if restarted.AccountsManagerSwitchCanRetry(final) || restarted.AccountsManagerSwitchCanRetry(op) {
						t.Fatal("settled or stale observation advertised retry")
					}
					binding, found, err := reopened.GetAccountsManagerSessionRoute(t.Context(), rec.ID, provider)
					if err != nil || !found {
						t.Fatal("binding missing", err)
					}
					if action == "cancel" {
						if final.Phase != domain.AccountsManagerSwitchCancelled || binding.AccountID != "account-a" || binding.Revision != cfg.ExpectedRevision || rt.created != 0 || rt.destroyed != 0 {
							t.Fatal("cancelled retry mutated account or runtime")
						}
					} else if final.Phase != domain.AccountsManagerSwitchReady || binding.AccountID != cfg.AccountID || rt.created != 1 || rt.destroyed != 1 {
						t.Fatalf("explicit retry failed: phase=%s code=%s", final.Phase, final.ErrorCode)
					}
				})
			}
		}
	}
}

func TestAccountsManagerSwitchRetryCapability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		phase     domain.AccountsManagerSwitchPhase
		run       *accountsManagerSwitchRun
		shutdown  bool
		closed    bool
		wantRetry bool
	}{
		{"requested", domain.AccountsManagerSwitchRequested, &accountsManagerSwitchRun{id: "operation"}, false, false, true},
		{"waiting", domain.AccountsManagerSwitchWaiting, &accountsManagerSwitchRun{id: "operation"}, false, false, true},
		{"recovery", domain.AccountsManagerSwitchRecoveryRequired, &accountsManagerSwitchRun{id: "operation"}, false, false, true},
		{"active", domain.AccountsManagerSwitchWaiting, &accountsManagerSwitchRun{id: "operation", running: true}, false, false, false},
		{"cancelled-run", domain.AccountsManagerSwitchWaiting, &accountsManagerSwitchRun{id: "operation", cancelled: true}, false, false, false},
		{"foreign", domain.AccountsManagerSwitchWaiting, &accountsManagerSwitchRun{id: "replacement"}, false, false, false},
		{"missing", domain.AccountsManagerSwitchWaiting, nil, false, false, false},
		{"stopping", domain.AccountsManagerSwitchStopping, &accountsManagerSwitchRun{id: "operation"}, false, false, false},
		{"ready", domain.AccountsManagerSwitchReady, &accountsManagerSwitchRun{id: "operation"}, false, false, false},
		{"cancelled", domain.AccountsManagerSwitchCancelled, &accountsManagerSwitchRun{id: "operation"}, false, false, false},
		{"failed", domain.AccountsManagerSwitchFailed, &accountsManagerSwitchRun{id: "operation"}, false, false, false},
		{"unknown", "future", &accountsManagerSwitchRun{id: "operation"}, false, false, false},
		{"shutdown", domain.AccountsManagerSwitchWaiting, &accountsManagerSwitchRun{id: "operation"}, true, false, false},
		{"closed", domain.AccountsManagerSwitchWaiting, &accountsManagerSwitchRun{id: "operation"}, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.shutdown {
				cancel()
			}
			m := New(Deps{BackgroundContext: ctx})
			m.accountSwitches = map[domain.SessionID]*accountsManagerSwitchRun{"session": tc.run}
			m.agentSwitchWorkersClosed = tc.closed
			reader, ok := any(m).(accountRetryCapability)
			if !ok {
				t.Fatal("manager does not expose recovered switch retry eligibility")
			}
			op := domain.AccountsManagerSwitch{ID: "operation", SessionID: "session", Phase: tc.phase}
			if got := reader.AccountsManagerSwitchCanRetry(op); got != tc.wantRetry {
				t.Fatalf("canRetry=%t want=%t", got, tc.wantRetry)
			}
			op.SessionID = "foreign-session"
			if reader.AccountsManagerSwitchCanRetry(op) {
				t.Fatal("foreign session inherited retry availability")
			}
		})
	}
}
