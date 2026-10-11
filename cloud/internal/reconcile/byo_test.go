package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// byoProvider is a Coder-like provider that counts the calls that would touch
// a customer's workspace and returns a scripted bootstrap result.
type byoProvider struct {
	lifecycleProvider
	stops        int
	deletes      int
	bootstraps   []sandbox.WorkerBootstrap
	bootstrapErr error
}

func (p *byoProvider) Stop(context.Context, sandbox.ID) error {
	p.stops++
	return nil
}

func (p *byoProvider) Delete(context.Context, sandbox.ID) error {
	p.deletes++
	return nil
}

func (p *byoProvider) BootstrapWorker(_ context.Context, _ sandbox.ID, bootstrap sandbox.WorkerBootstrap) error {
	p.bootstraps = append(p.bootstraps, bootstrap)
	return p.bootstrapErr
}

func coderRecord(t *testing.T, connectionID string, profile map[string]any, startedAgo time.Duration) domain.Sandbox {
	t.Helper()
	coder := map[string]any{
		"baseUrl": "https://coder.example.com", "owner": "ahmad",
		"templateId": "2a2e262c-b31c-4202-946d-a19ad45d1fd2", "durableRoot": "$HOME",
	}
	for key, value := range profile {
		coder[key] = value
	}
	raw, err := json.Marshal(map[string]any{"provider": "coder", "coder": coder})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().Add(-startedAgo)
	return domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderCoder,
		ProviderEnvironmentID: "workspace-1", ProviderConnectionID: connectionID,
		DesiredState:     domain.SandboxDesiredRunning,
		ObservedState:    domain.SandboxObservedProvisioning,
		ResourceProfile:  raw,
		StartupStartedAt: &startedAt,
		UpdatedAt:        startedAt,
	}
}

func byoReconciler(store Store, provider sandbox.Provider) *Reconciler {
	return New(store, lifecycleResolver{provider: provider}, Options{
		Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartupTimeout:         3 * time.Minute,
		TerminalStartupTimeout: 10 * time.Minute,
		WorkerBinary:           []byte("amd64-worker"),
		WorkerHelperBinary:     []byte("amd64-helper"),
		WorkerBuilds: map[string]sandbox.WorkerBuild{
			sandbox.ArchARM64: {Binary: []byte("arm64-worker"), HelperBinary: []byte("arm64-helper")},
		},
	})
}

var byoBudget = map[string]any{"startupTimeoutSeconds": 1200}

// The incident: a BYO workspace whose blocking startup script takes ~6 minutes
// must not be reported failed after the deployment's 3-minute budget, nor
// stopped at the 10-minute ceiling. Its connection's 20-minute budget governs.
func TestBYOStartupBudgetOverridesDeploymentTimeouts(t *testing.T) {
	store := &lifecycleStore{}
	provider := &byoProvider{lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateProvisioning,
	}}}
	record := coderRecord(t, "connection-1", byoBudget, 15*time.Minute)
	if err := byoReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.failures) != 0 || provider.stops != 0 {
		t.Fatalf("failures = %v, stops = %d; want neither inside the 20-minute budget", store.failures, provider.stops)
	}
	if len(store.observations) != 1 || store.observations[0] != domain.SandboxObservedProvisioning {
		t.Fatalf("observations = %v, want provisioning", store.observations)
	}

	// The deployment Coder keeps its own 3-minute budget for the same age.
	deployment := coderRecord(t, "", nil, 5*time.Minute)
	store = &lifecycleStore{}
	if err := byoReconciler(store, provider).reconcileSandbox(context.Background(), deployment); err == nil ||
		len(store.failures) != 1 || !strings.Contains(store.failures[0], "3 minutes") {
		t.Fatalf("deployment Coder failures = %v, want the 3-minute ready timeout", store.failures)
	}
}

func TestBYOCeilingParksWithoutStoppingWorkspace(t *testing.T) {
	for name, state := range map[string]string{
		"workspace never ready": sandbox.StateProvisioning,
		"worker never started":  sandbox.StateRunning,
	} {
		t.Run(name, func(t *testing.T) {
			store := &lifecycleStore{}
			provider := &byoProvider{lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
				ID: "workspace-1", State: state,
			}}}
			record := coderRecord(t, "connection-1", byoBudget, 21*time.Minute)
			if err := byoReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if provider.stops != 0 || provider.deletes != 0 {
				t.Fatalf("stops = %d, deletes = %d; AO must never stop a BYO workspace on failure",
					provider.stops, provider.deletes)
			}
			if len(store.observations) != 1 || store.observations[0] != domain.SandboxObservedTerminated {
				t.Fatalf("observations = %v, want terminated", store.observations)
			}
			if len(store.startupErrors) != 1 || !strings.Contains(store.startupErrors[0].message, "20 minutes") ||
				!strings.Contains(store.startupErrors[0].message, "left the workspace running") {
				t.Fatalf("startup errors = %+v, want a 20-minute explanation", store.startupErrors)
			}
			wantCode := sandbox.StartupErrorWorkerNeverStarted
			if state == sandbox.StateProvisioning {
				wantCode = sandbox.StartupErrorWorkspaceNotReady
				if store.startupErrors[0].message != "Your Coder workspace wasn't ready after 20 minutes. AO left the workspace running so you can inspect it." {
					t.Fatalf("message = %q", store.startupErrors[0].message)
				}
			}
			if store.startupErrors[0].code != wantCode {
				t.Fatalf("code = %q, want %q", store.startupErrors[0].code, wantCode)
			}
		})
	}
}

// AO-operated compute keeps the existing behavior: past the ceiling it is
// stopped so billing halts.
func TestDeploymentCeilingStillStopsCompute(t *testing.T) {
	store := &lifecycleStore{}
	provider := &byoProvider{lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateRunning,
	}}}
	record := coderRecord(t, "", nil, 11*time.Minute)
	if err := byoReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.stops != 1 {
		t.Fatalf("stops = %d, want 1", provider.stops)
	}
}

// A specific reason recorded during the attempts survives the ceiling instead
// of being replaced by the generic message.
func TestCeilingKeepsSpecificStartupError(t *testing.T) {
	store := &lifecycleStore{}
	provider := &byoProvider{lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateRunning,
	}}}
	record := coderRecord(t, "connection-1", byoBudget, 21*time.Minute)
	record.StartupErrorCode = sandbox.StartupErrorTerminalUnavailable
	if err := byoReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.startupErrors) != 0 {
		t.Fatalf("startup errors = %+v, want the earlier specific error kept", store.startupErrors)
	}
}

// A terminal that closed before the bootstrap began is retry-later: the
// sandbox stays provisioning (not failed), no repair is counted, and the user
// sees why.
func TestNotReadyBootstrapRetriesWithoutFailing(t *testing.T) {
	store := &lifecycleStore{}
	notReady := &sandbox.StartupError{
		Code:    sandbox.StartupErrorTerminalUnavailable,
		Message: "AO couldn't open a terminal in the workspace yet; it may still be starting up. AO will keep retrying.",
		Err:     errors.Join(sandbox.ErrWorkspaceNotReady, errors.New("EOF")),
	}
	provider := &byoProvider{
		lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateRunning}},
		bootstrapErr:      fmt.Errorf("coder: launch preinstalled worker: %w", notReady),
	}
	record := coderRecord(t, "connection-1", byoBudget, 7*time.Minute)
	if err := byoReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.failures) != 0 || store.repairs != 0 {
		t.Fatalf("failures = %v, repairs = %d; want a quiet retry", store.failures, store.repairs)
	}
	if len(store.observations) != 1 || store.observations[0] != domain.SandboxObservedProvisioning {
		t.Fatalf("observations = %v, want provisioning", store.observations)
	}
	if len(store.startupErrors) != 1 || store.startupErrors[0].code != sandbox.StartupErrorTerminalUnavailable {
		t.Fatalf("startup errors = %+v, want terminal_unavailable", store.startupErrors)
	}
}

func TestUnsupportedArchitectureParksImmediately(t *testing.T) {
	store := &lifecycleStore{}
	provider := &byoProvider{
		lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateRunning}},
		bootstrapErr: &sandbox.StartupError{
			Code:    sandbox.StartupErrorUnsupportedArchitecture,
			Message: "This workspace's CPU architecture (riscv64) isn't supported.",
		},
	}
	record := coderRecord(t, "connection-1", byoBudget, time.Minute)
	if err := byoReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.observations) != 1 || store.observations[0] != domain.SandboxObservedTerminated || provider.stops != 0 {
		t.Fatalf("observations = %v, stops = %d; want parked without a stop", store.observations, provider.stops)
	}
	if len(store.startupErrors) != 1 || store.startupErrors[0].code != sandbox.StartupErrorUnsupportedArchitecture {
		t.Fatalf("startup errors = %+v", store.startupErrors)
	}
}

func TestWorkerBootstrapCarriesArchBuildsAndMountPolicy(t *testing.T) {
	reconciler := byoReconciler(&lifecycleStore{}, nil)
	byo := reconciler.workerBootstrap(coderRecord(t, "connection-1", nil, 0), sandbox.Spec{}, false)
	if byo.RequireMountedDurableRoot {
		t.Fatal("BYO bootstrap requires a mounted durable root")
	}
	if string(byo.Builds[sandbox.ArchARM64].Binary) != "arm64-worker" ||
		string(byo.Builds[sandbox.ArchAMD64].Binary) != "amd64-worker" {
		t.Fatalf("builds = %+v, want amd64 and arm64", byo.Builds)
	}
	deployment := reconciler.workerBootstrap(coderRecord(t, "", nil, 0), sandbox.Spec{}, false)
	if !deployment.RequireMountedDurableRoot {
		t.Fatal("deployment bootstrap dropped the mounted durable root check")
	}
	optedIn := reconciler.workerBootstrap(
		coderRecord(t, "connection-1", map[string]any{"requireMountedDurableRoot": true}, 0), sandbox.Spec{}, false)
	if !optedIn.RequireMountedDurableRoot {
		t.Fatal("a BYO connection that opted into the mount check lost it")
	}
}

func TestHumanDuration(t *testing.T) {
	for duration, want := range map[time.Duration]string{
		20 * time.Minute: "20 minutes",
		time.Minute:      "1 minute",
		90 * time.Second: "1m30s",
	} {
		if got := humanDuration(duration); got != want {
			t.Errorf("humanDuration(%s) = %q, want %q", duration, got, want)
		}
	}
}
