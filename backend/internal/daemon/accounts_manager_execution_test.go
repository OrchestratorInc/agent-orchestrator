package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"sync"
	"testing"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type publicExecutionCatalog struct {
	*publicControlCatalog
}

func (c *publicExecutionCatalog) MintRoute(_ context.Context, provider core.Provider, ref, session, account string, revision int64) (core.RouteCapability, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, binding := range c.bindings.Bindings {
		if !binding.Blocked && binding.SessionID == session && binding.Provider == string(provider) && binding.AccountID == account && ref == account && binding.Revision == revision {
			return core.RouteCapability{BaseURL: "http://127.0.0.1:12345", Token: fmt.Sprintf("synthetic-route-%s-%d", account, revision)}, nil
		}
	}
	return core.RouteCapability{}, core.ErrCredentialConflict
}

type publicExecutionAgent struct {
	ports.Agent
	mu       sync.Mutex
	binary   string
	launches []ports.LaunchConfig
}

func (a *publicExecutionAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.launches = append(a.launches, cfg)
	return []string{a.binary}, nil
}

func (*publicExecutionAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error {
	return nil
}

func (*publicExecutionAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}

func (*publicExecutionAgent) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	return (&codex.Plugin{}).InspectTerminalSurface(output)
}

type publicExecutionAgents struct{ agent ports.Agent }

func (a publicExecutionAgents) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	return a.agent, harness == domain.HarnessCodex
}

type publicExecutionRuntime struct {
	*publicControlRuntime
	launches []ports.RuntimeConfig
	ready    bool
	failNext bool
}

func (r *publicExecutionRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.launches = append(r.launches, cfg)
	if r.failNext {
		r.failNext = false
		return ports.RuntimeHandle{}, errors.New("synthetic target launch failure")
	}
	handle := ports.RuntimeHandle{ID: "target-" + string(cfg.SessionID)}
	r.owners[handle.ID] = ports.FencedRuntimeRef{SessionID: cfg.SessionID, Handle: handle, Generation: cfg.Env[sessionmanager.EnvRuntimeLaunchID]}
	return handle, nil
}

func (r *publicExecutionRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ready {
		return "provider is starting", nil
	}
	return "\x1b[1m›\x1b[0m \x1b[2mWrite tests for @filename\x1b[0m\n\ngpt-5.6-sol low · ~/project\n", nil
}

func (r *publicExecutionRuntime) IsExactSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	probe := r.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{Handle: handle, SessionID: ref.SessionID, Generation: ref.LaunchID})
	if probe.Liveness == ports.FencedUnknown {
		return false, errors.New("synthetic exact owner is unknown")
	}
	return probe.Liveness == ports.FencedAlive, nil
}

func newPublicExecutionFixture(t *testing.T) (*publicControlFixture, *publicExecutionRuntime, *publicExecutionAgent) {
	t.Helper()
	return newPublicExecutionFixtureWithRuntime(t, nil)
}

func newPublicExecutionFixtureWithRuntime(t *testing.T, wrap func(*publicExecutionRuntime) runtimeselect.Runtime) (*publicControlFixture, *publicExecutionRuntime, *publicExecutionAgent) {
	t.Helper()
	binary, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	store := sqlitetest.MustOpen(t)
	cfg := config.Config{DataDir: t.TempDir()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runtime := &publicExecutionRuntime{publicControlRuntime: &publicControlRuntime{owners: make(map[string]ports.FencedRuntimeRef)}}
	agent := &publicExecutionAgent{Agent: &codex.Plugin{}, binary: binary}
	catalog := &publicExecutionCatalog{publicControlCatalog: &publicControlCatalog{credentials: []core.CredentialSummary{
		{Ref: "account-a", Provider: core.ProviderCodex, Kind: core.CredentialOAuth, Status: core.CredentialActive},
		{Ref: "account-b", Provider: core.ProviderCodex, Kind: core.CredentialOAuth, Status: core.CredentialActive},
	}}}
	accounts := accountsvc.New(catalog, store)
	ctx, cancel := context.WithCancel(t.Context())
	selected := runtimeselect.Runtime(runtime)
	if wrap != nil {
		selected = wrap(runtime)
	}
	svc, _, owner, err := startSession(ctx, cfg, selected, store, lifecycle.New(store, nil), newSessionMessenger(store, selected, log), telemetryadapter.NoopSink{}, publicExecutionAgents{agent}, nil, nil, nil, nil, nil, nil, nil, nil, nil, accounts, log)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		join, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := owner.WaitAgentSwitchWorkers(join); err != nil {
			t.Error(err)
		}
	})
	controls, _ := any(svc).(controllers.AccountsManagerControls)
	router := httpd.NewRouterWithControl(cfg, log, nil, httpd.APIDeps{Sessions: svc, AccountsManagerService: accounts, AccountsManagerControls: controls}, httpd.ControlDeps{})
	return &publicControlFixture{store: store, manager: owner.(*sessionmanager.Manager), router: router, runtime: runtime.publicControlRuntime, catalog: catalog.publicControlCatalog}, runtime, agent
}

func awaitPublicExecutionPhase(t *testing.T, f *publicControlFixture, id string, phase domain.AccountsManagerSwitchPhase) domain.AccountsManagerSwitch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, found, err := f.store.GetAccountsManagerSwitch(t.Context(), id)
		if err != nil || !found {
			t.Fatalf("operation missing: found=%v err=%v", found, err)
		}
		if op.Phase == phase {
			return op
		}
		if op.Phase.Terminal() || op.Phase == domain.AccountsManagerSwitchRecoveryRequired || time.Now().After(deadline) {
			t.Fatalf("switch phase=%s code=%s, want=%s", op.Phase, op.ErrorCode, phase)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAccountsManagerControlProductionExecution(t *testing.T) {
	for _, target := range []domain.AccountsManagerConnectionMode{domain.AccountsManagerManaged, domain.AccountsManagerNative} {
		t.Run(string(target), func(t *testing.T) {
			f, runtime, agent := newPublicExecutionFixture(t)
			binding := f.seed(t, domain.AccountsManagerManaged, "account-a")
			unrelated := f.seed(t, domain.AccountsManagerNative, "")
			record, _, err := f.store.GetSession(t.Context(), binding.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			record.Activity.State = domain.ActivityExited
			record.Metadata.Prompt = "saved task must not replay"
			if err := f.store.UpdateSession(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			account := ""
			if target == domain.AccountsManagerManaged {
				account = "account-b"
			}
			path := "/sessions/" + string(binding.SessionID) + "/account-switches"
			body := fmt.Sprintf(`{"operationId":"execute-switch","expectedRevision":%d,"mode":%q,"accountId":%q,"policy":"interrupt","newConversation":true}`, binding.Revision, target, account)
			f.request(t, http.MethodPost, path, body, http.StatusAccepted)
			starting := awaitPublicExecutionPhase(t, f, "execute-switch", domain.AccountsManagerSwitchStarting)
			if release, allowed := f.manager.AcquireSessionInput(binding.SessionID); allowed {
				release()
				t.Fatal("input opened before target readiness")
			}
			res := f.request(t, http.MethodGet, "/sessions/"+string(binding.SessionID)+"/account", "", http.StatusOK)
			var view controllers.AccountsManagerSessionResponse
			if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			if view.Mode != string(target) || view.AccountID != account || view.Revision != starting.TargetRevision || view.Switch == nil || view.Switch.Phase != "starting" {
				t.Fatal("public projection confused committed account with unready target")
			}
			runtime.mu.Lock()
			runtime.ready = true
			runtime.mu.Unlock()
			ready := awaitPublicExecutionPhase(t, f, starting.ID, domain.AccountsManagerSwitchReady)
			f.request(t, http.MethodPost, path, body, http.StatusAccepted)
			current, _, err := f.store.GetSession(t.Context(), binding.SessionID)
			if err != nil || current.Metadata.RuntimeLaunchID != ready.TargetGeneration || current.Metadata.Prompt != record.Metadata.Prompt {
				t.Fatal("target identity or saved task was lost")
			}
			preserved, found, err := f.store.GetAccountsManagerSessionRoute(t.Context(), unrelated.SessionID, unrelated.Provider)
			if err != nil || !found || preserved != unrelated {
				t.Fatal("unrelated native binding changed")
			}
			agent.mu.Lock()
			if len(agent.launches) != 1 || agent.launches[0].Prompt != "" || (agent.launches[0].Route != nil) != (target == domain.AccountsManagerManaged) {
				t.Error("launch replayed the task or lost explicit routing mode")
			}
			agent.mu.Unlock()
			runtime.mu.Lock()
			if len(runtime.launches) != 1 || runtime.destroyed != 1 || len(runtime.owners) != 2 {
				t.Error("switch did not stop and replace only the recorded source once")
			}
			runtime.mu.Unlock()
		})
	}
}
