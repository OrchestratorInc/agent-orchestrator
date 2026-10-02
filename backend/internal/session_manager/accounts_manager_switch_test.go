package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type accountSwitchRouter struct {
	store    *sqlite.Store
	validate func(context.Context) error
	syncErr  error
	syncHook func(context.Context) error
	routeErr error
}

func (r *accountSwitchRouter) PrepareAgentLaunchRoute(ctx context.Context, id domain.SessionID, provider domain.AccountsManagerProvider, _ string) (*ports.AccountsManagerLaunchRoute, error) {
	if r.routeErr != nil {
		return nil, r.routeErr
	}
	binding, _, err := r.store.GetAccountsManagerSessionRoute(ctx, id, provider)
	if err != nil || binding.Mode == domain.AccountsManagerNative {
		return nil, err
	}
	return &ports.AccountsManagerLaunchRoute{BaseURL: "http://127.0.0.1:12345", Token: binding.AccountID}, nil
}
func (*accountSwitchRouter) AgentRoutingEnabled(context.Context, domain.AccountsManagerProvider) (bool, error) {
	return true, nil
}
func (*accountSwitchRouter) HasAgentSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (bool, error) {
	return true, nil
}
func (r *accountSwitchRouter) AdmitAgentAccountSwitch(ctx context.Context, op domain.AccountsManagerSwitch, _ string) (domain.AccountsManagerSwitch, bool, error) {
	return r.store.CreateAccountsManagerSwitch(ctx, op)
}
func (r *accountSwitchRouter) ValidateAgentAccountTarget(ctx context.Context, _ domain.AccountsManagerConnectionMode, _ domain.AccountsManagerProvider, _, _ string) error {
	if r.validate != nil {
		return r.validate(ctx)
	}
	return nil
}
func (r *accountSwitchRouter) CommitAgentAccountSwitch(ctx context.Context, id, _ string) (domain.AccountsManagerSwitch, error) {
	return r.store.CommitAccountsManagerSwitch(ctx, id)
}
func (r *accountSwitchRouter) SynchronizeAgentBindings(ctx context.Context) error {
	if r.syncHook != nil {
		return r.syncHook(ctx)
	}
	return r.syncErr
}

type accountSwitchAgent struct{ recordingAgent }

type accountSwitchRuntime struct{ *fakeRuntime }

func (r accountSwitchRuntime) LaunchHandles(domain.SessionID) ([]ports.RuntimeHandle, error) {
	return []ports.RuntimeHandle{{ID: "h1"}}, nil
}

func (r accountSwitchRuntime) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	return r.GetOutput(ctx, handle, lines)
}

func (*accountSwitchAgent) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	return (transitionSurfaceAgent{}).InspectTerminalSurface(output)
}

func accountSwitchFixture(t *testing.T) (*Manager, *sqlite.Store, *fakeRuntime, *accountSwitchAgent, domain.SessionRecord, AccountsManagerSwitchConfig) {
	t.Helper()
	return accountSwitchFixtureMode(t, domain.SessionModeTUI)
}

func accountSwitchFixtureMode(t *testing.T, mode domain.SessionMode) (*Manager, *sqlite.Store, *fakeRuntime, *accountSwitchAgent, domain.SessionRecord, AccountsManagerSwitchConfig) {
	t.Helper()
	path := t.TempDir()
	st := sqlitetest.MustOpenAt(t, path)
	if err := st.UpsertProject(t.Context(), domain.ProjectRecord{ID: "accounts", Path: path, Config: testRoleAgents(), RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "accounts", Harness: domain.HarnessClaudeCode, Mode: mode, Kind: domain.KindWorker,
		CreatedAt: time.Now(), Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "source", RuntimeLaunchID: "source-generation", WorkspacePath: path, Prompt: "never replay this task"}})
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: rec.ID, Provider: domain.AccountsManagerProviderClaude, Mode: domain.AccountsManagerManaged, AccountID: "account-a"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{aliveByHandle: map[string]bool{"source": true}, outputs: []string{idleTerminalOutput}}
	agent := &accountSwitchAgent{}
	m := New(Deps{Store: st, Runtime: accountSwitchRuntime{runtime}, Lifecycle: lifecycle.New(st, nil), AccountsManager: &accountSwitchRouter{store: st},
		Agents: singleAgent{agent: agent}, DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil }, BackgroundContext: t.Context()})
	m.switchTargetStartWait = time.Second
	m.interfaceTransition.pollInterval = time.Millisecond
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := m.WaitAgentSwitchWorkers(ctx); err != nil {
			t.Error(err)
		}
	})
	return m, st, runtime, agent, rec, AccountsManagerSwitchConfig{OperationID: "switch-test", ExpectedRevision: binding.Revision, Mode: domain.AccountsManagerManaged, AccountID: "account-b", Policy: domain.SessionInterfaceTransitionInterrupt, NewConversation: true}
}

func waitAccountSwitch(t *testing.T, m *Manager, st *sqlite.Store, id string) domain.AccountsManagerSwitch {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	m.accountSwitchMu.Lock()
	var done <-chan struct{}
	for _, run := range m.accountSwitches {
		if run.id == id {
			done = run.done
			break
		}
	}
	m.accountSwitchMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("switch worker did not settle")
		}
	}
	op, found, err := st.GetAccountsManagerSwitch(ctx, id)
	if err != nil || !found {
		t.Fatalf("operation: found=%v err=%v", found, err)
	}
	return op
}

func TestAccountsManagerSwitchCommitsOnlyAfterStopAndReadiness(t *testing.T) {
	m, st, rt, agent, rec, cfg := accountSwitchFixture(t)
	rt.onDestroy = func(_ int, _ ports.RuntimeHandle) {
		snapshot, err := st.AccountsManagerBindings(t.Context())
		if err != nil || !snapshot.Bindings[0].Blocked || snapshot.Bindings[0].AccountID != "account-a" {
			t.Error("source was not durably fenced before stopping")
		}
		if release, ok := m.AcquireSessionInput(rec.ID); ok {
			release()
			t.Error("input admitted while stopping")
		}
		if release, ok := m.AcquireSessionInput("another-session"); !ok {
			t.Error("unrelated session was fenced")
		} else {
			release()
		}
	}
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	op := waitAccountSwitch(t, m, st, cfg.OperationID)
	if op.Phase != domain.AccountsManagerSwitchReady {
		t.Fatalf("phase=%s code=%s", op.Phase, op.ErrorCode)
	}
	if rt.created != 1 || rt.destroyed != 1 || rt.lastCfg.Env["ANTHROPIC_AUTH_TOKEN"] != cfg.AccountID {
		t.Fatal("wrong source/target execution")
	}
	if agent.lastLaunch.Prompt != "" || agent.lastLaunch.Route == nil {
		t.Fatal("prompt was replayed or route omitted")
	}
	current, _, err := st.GetSession(t.Context(), rec.ID)
	if err != nil || current.Metadata.Prompt != rec.Metadata.Prompt || current.Metadata.RuntimeLaunchID != op.TargetGeneration {
		t.Fatal("task metadata or target identity lost")
	}
	if release, ok := m.AcquireSessionInput(rec.ID); !ok {
		t.Fatal("ready target input remains closed")
	} else {
		release()
	}
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal("retry was not idempotent", err)
	}
	if rt.created != 1 {
		t.Fatal("duplicate request launched another controller")
	}
}

func TestAccountsManagerSwitchUncertaintyRetainsBindingAndInputFence(t *testing.T) {
	for _, scenario := range []string{"ownership", "stop", "sync", "start", "ready"} {
		t.Run(scenario, func(t *testing.T) {
			m, st, rt, _, rec, cfg := accountSwitchFixture(t)
			switch scenario {
			case "ownership":
				rt.fencedResult = ports.FencedProbeResult{Liveness: ports.FencedUnknown}
			case "stop":
				rt.destroyErr = errors.New("stop unavailable")
			case "sync":
				m.accountsManager.(*accountSwitchRouter).syncErr = errors.New("runner unavailable")
			case "start":
				rt.createErr = errors.New("start unavailable")
			case "ready":
				rt.outputs = []string{ambiguousTerminalOutput}
				m.switchTargetStartWait = 30 * time.Millisecond
			}
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
				t.Fatal(err)
			}
			op := waitAccountSwitch(t, m, st, cfg.OperationID)
			if op.Phase != domain.AccountsManagerSwitchRecoveryRequired {
				t.Fatalf("phase=%s code=%s", op.Phase, op.ErrorCode)
			}
			snapshot, err := st.AccountsManagerBindings(t.Context())
			want := "account-a"
			if scenario == "start" || scenario == "ready" {
				want = "account-b"
			}
			if err != nil || !snapshot.Bindings[0].Blocked || snapshot.Bindings[0].AccountID != want {
				t.Fatalf("binding=%+v err=%v", snapshot, err)
			}
			if release, ok := m.AcquireSessionInput(rec.ID); ok {
				release()
				t.Fatal("uncertain operation reopened input")
			}
			if scenario == "ownership" && rt.destroyed != 0 {
				t.Fatal("unknown ownership was destroyed")
			}
			if _, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
				t.Fatal("stopping operation was cancellable")
			}
			restarted := New(Deps{Store: st, AccountsManager: m.accountsManager})
			if err := restarted.reconcileAccountsManagerSwitches(t.Context()); err != nil {
				t.Fatal(err)
			}
			if release, ok := restarted.AcquireSessionInput(rec.ID); ok {
				release()
				t.Fatal("restart lost recovery fence")
			}
		})
	}
}

func TestAccountsManagerSwitchCancellationPreservesSource(t *testing.T) {
	m, st, rt, _, rec, cfg := accountSwitchFixture(t)
	validating := make(chan struct{})
	m.accountsManager.(*accountSwitchRouter).validate = func(ctx context.Context) error { close(validating); <-ctx.Done(); return ctx.Err() }
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	select {
	case <-validating:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not validate")
	}
	if _, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	op := waitAccountSwitch(t, m, st, cfg.OperationID)
	if op.Phase != domain.AccountsManagerSwitchCancelled || rt.destroyed != 0 || rt.created != 0 {
		t.Fatal("cancellation changed the controller")
	}
	if release, ok := m.AcquireSessionInput(rec.ID); !ok {
		t.Fatal("source input not restored")
	} else {
		release()
	}
}

func TestAccountsManagerSwitchRetryRotatesGenerationAndAuthorization(t *testing.T) {
	m, st, rt, agent, rec, cfg := accountSwitchFixture(t)
	rt.outputs = []string{ambiguousTerminalOutput}
	m.switchTargetStartWait = 30 * time.Millisecond
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	previous := waitAccountSwitch(t, m, st, cfg.OperationID)
	if previous.Phase != domain.AccountsManagerSwitchRecoveryRequired || previous.TargetRevision <= 0 {
		t.Fatal("missing committed recovery obligation")
	}
	rt.outputs = []string{idleTerminalOutput}
	m.switchTargetStartWait = time.Second
	if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	op := waitAccountSwitch(t, m, st, cfg.OperationID)
	if op.Phase != domain.AccountsManagerSwitchReady {
		t.Fatalf("phase=%s code=%s", op.Phase, op.ErrorCode)
	}
	if op.TargetGeneration == previous.TargetGeneration || op.TargetRevision <= previous.TargetRevision {
		t.Fatal("retry reused generation or old route authorization")
	}
	if rt.created != 2 || rt.destroyed != 2 || agent.lastLaunch.Prompt != "" {
		t.Fatal("retry did not replace only the intended target without replay")
	}
}

func TestAccountsManagerSwitchRetryDoesNotCommitWhileOwnershipUnknown(t *testing.T) {
	m, st, rt, _, rec, cfg := accountSwitchFixture(t)
	rt.fencedResult = ports.FencedProbeResult{Liveness: ports.FencedUnknown}
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	previous := waitAccountSwitch(t, m, st, cfg.OperationID)
	if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	again := waitAccountSwitch(t, m, st, cfg.OperationID)
	if again.Phase != domain.AccountsManagerSwitchRecoveryRequired || again.TargetRevision != 0 || previous.TargetRevision != 0 || rt.created != 0 || rt.destroyed != 0 {
		t.Fatal("retry guessed ownership")
	}
	rt.fencedResult = ports.FencedProbeResult{}
	if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	if op := waitAccountSwitch(t, m, st, cfg.OperationID); op.Phase != domain.AccountsManagerSwitchReady {
		t.Fatalf("recovered phase=%s code=%s", op.Phase, op.ErrorCode)
	}
}

func TestAccountsManagerSwitchCanRetryAgainAfterRotatedLaunchFails(t *testing.T) {
	m, st, rt, _, rec, cfg := accountSwitchFixture(t)
	rt.outputs = []string{ambiguousTerminalOutput}
	m.switchTargetStartWait = 30 * time.Millisecond
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	first := waitAccountSwitch(t, m, st, cfg.OperationID)
	if first.Phase != domain.AccountsManagerSwitchRecoveryRequired {
		t.Fatal("missing first recovery obligation")
	}
	rt.outputs = []string{idleTerminalOutput}
	rt.createErr = errors.New("replacement launch failed after generation rotation")
	if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	failed := waitAccountSwitch(t, m, st, cfg.OperationID)
	current, _, err := st.GetSession(t.Context(), rec.ID)
	if err != nil || failed.TargetGeneration == first.TargetGeneration || current.Metadata.RuntimeLaunchID != first.TargetGeneration {
		t.Fatal("regression did not reach generation rotation before failed launch")
	}
	rt.createErr = nil
	m.switchTargetStartWait = time.Second
	probeStart := len(rt.fencedRefs)
	if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	if op := waitAccountSwitch(t, m, st, cfg.OperationID); op.Phase != domain.AccountsManagerSwitchReady {
		t.Fatalf("second retry stranded: phase=%s code=%s", op.Phase, op.ErrorCode)
	}
	for _, ref := range rt.fencedRefs[probeStart:] {
		if ref.Generation != first.TargetGeneration && ref.Generation != failed.TargetGeneration {
			t.Fatalf("retry probed an unjournaled generation %q", ref.Generation)
		}
	}
}

func TestAccountsManagerSwitchUntouchedSessionUsesProvenFreshLaunch(t *testing.T) {
	m, st, _, _, rec, cfg := accountSwitchFixture(t)
	m.agents = singleAgent{agent: untouchedEmptyTransitionAgent{}}
	rec.Metadata.Prompt = ""
	rec.Metadata.AgentSessionID = "reserved-without-history"
	rec.Metadata.AgentSessionIDLaunchID = rec.Metadata.RuntimeLaunchID
	if err := st.UpdateSession(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if id, err := m.handoffNativeConversationID(t.Context(), rec); err != nil || id != "" {
		t.Fatalf("fixture lacks positive empty proof: id=%q err=%v", id, err)
	}
	cfg.NewConversation = false
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if op := waitAccountSwitch(t, m, st, cfg.OperationID); op.Phase != domain.AccountsManagerSwitchReady {
		t.Fatalf("untouched session stranded: phase=%s code=%s", op.Phase, op.ErrorCode)
	}
}

type accountSwitchExitedPaneRuntime struct {
	accountSwitchRuntime
	source string
}

func (r accountSwitchExitedPaneRuntime) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	if ref.Handle.ID == r.source {
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	return r.accountSwitchRuntime.ProbeFencedRuntime(ctx, ref)
}
func (r accountSwitchExitedPaneRuntime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	if r.aliveByHandle[r.source] {
		return ports.RuntimeHandle{}, errors.New("source scrollback pane still occupies runtime slot")
	}
	return r.accountSwitchRuntime.Create(ctx, cfg)
}

func TestAccountsManagerSwitchRetiresExitedSourcePaneBeforeReplacement(t *testing.T) {
	m, st, rt, _, rec, cfg := accountSwitchFixture(t)
	m.runtime = accountSwitchExitedPaneRuntime{accountSwitchRuntime{rt}, rec.Metadata.RuntimeHandleID}
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if op := waitAccountSwitch(t, m, st, cfg.OperationID); op.Phase != domain.AccountsManagerSwitchReady {
		t.Fatalf("exited source blocked replacement: phase=%s code=%s", op.Phase, op.ErrorCode)
	}
}
