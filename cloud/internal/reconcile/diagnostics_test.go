package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/logctx"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

type recordedActivity struct {
	mu     sync.Mutex
	events []domain.ActivityEvent
}

func (r *recordedActivity) Record(_ context.Context, event domain.ActivityEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordedActivity) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	kinds := make([]string, 0, len(r.events))
	for _, event := range r.events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func (r *recordedActivity) find(kind string) (domain.ActivityEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event.Kind == kind {
			return event, true
		}
	}
	return domain.ActivityEvent{}, false
}

// detailStore adds the optional startup-detail capability to lifecycleStore.
type detailStore struct {
	lifecycleStore
	details []string
}

func (s *detailStore) RecordSandboxStartupErrorDetail(_ context.Context, _, _, _, detail string) error {
	s.details = append(s.details, detail)
	return nil
}

// diagnosingProvider reports bootstrap stages the way the Coder client does
// and can explain a failure with build and agent logs.
type diagnosingProvider struct {
	byoProvider
	stageErr  error
	diagnoses int
}

func (p *diagnosingProvider) BootstrapWorker(ctx context.Context, id sandbox.ID, bootstrap sandbox.WorkerBootstrap) error {
	sandbox.BeginStage(ctx, sandbox.StageAgentWait)(nil, map[string]string{"workspace_id": string(id)})
	finish := sandbox.BeginStage(ctx, sandbox.StageWorkerInstall)
	err := p.byoProvider.BootstrapWorker(ctx, id, bootstrap)
	finish(err, map[string]string{"workspace_id": string(id), "coder_owner": "dev"})
	return err
}

func (p *diagnosingProvider) Diagnose(context.Context, sandbox.ID) (sandbox.Diagnostics, error) {
	p.diagnoses++
	return sandbox.Diagnostics{
		Summary: map[string]string{
			"build_status": "running", "agent_lifecycle": "start_error", "coder_template": "devkit",
			"job_error": "",
		},
		BuildLog: []string{"[Setting up] terraform apply", "[Setting up] done"},
		AgentLog: []string{"cloning repo", "export GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123", "startup script exited 1"},
	}, nil
}

func TestStartupFailureCapturesStageDetailAndActivityOnce(t *testing.T) {
	var logs bytes.Buffer
	store := &detailStore{}
	bootstrapErr := &sandbox.StartupError{
		Code:    sandbox.StartupErrorBootstrapFailed,
		Message: "The AO worker could not be started in your workspace.",
		Err:     errors.New("coder: worker bootstrap failed: AO worker did not start; password=hunter2hunter2"),
	}
	provider := &diagnosingProvider{byoProvider: byoProvider{
		lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateRunning}},
		bootstrapErr:      fmt.Errorf("coder: launch preinstalled worker: %w", bootstrapErr),
	}}
	reconciler := byoReconciler(store, provider)
	reconciler.log = slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil)))
	activity := &recordedActivity{}
	reconciler.SetActivityRecorder(activity)

	record := coderRecord(t, "connection-1", byoBudget, 2*time.Minute)
	record.OrgID = "6f1c2c8e-8a39-4c55-9a5c-1d6a8c1f0a01"
	record.SessionID = "0b5e0f4e-2f8a-4f0c-9d2e-7a1b3c4d5e6f"
	if err := reconciler.reconcileClaim(context.Background(), record); err == nil {
		t.Fatal("a failed bootstrap must still fail the claim")
	}

	if len(store.details) != 1 {
		t.Fatalf("details = %v, want one capture", store.details)
	}
	detail := store.details[0]
	for _, want := range []string{"code: bootstrap_failed", "stage: worker_install", "agent_lifecycle: start_error",
		"--- build log (tail) ---", "startup script exited 1", "AO worker did not start"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail missing %q:\n%s", want, detail)
		}
	}
	if strings.Contains(detail, "ghp_abcdefghijklmnopqrstuvwxyz0123") || strings.Contains(detail, "hunter2hunter2") {
		t.Fatalf("detail leaked a secret:\n%s", detail)
	}
	if len(detail) > startupDetailLimit {
		t.Fatalf("detail is %d bytes, want <= %d", len(detail), startupDetailLimit)
	}

	failed, ok := activity.find(domain.ActivitySessionStartFailed)
	if !ok {
		t.Fatalf("activity = %v, want session.start_failed", activity.kinds())
	}
	if failed.Attrs["code"] != sandbox.StartupErrorBootstrapFailed || failed.Attrs["stage"] != sandbox.StageWorkerInstall ||
		failed.Attrs["connection_id"] != "connection-1" || failed.Provider != sandbox.ProviderCoder ||
		failed.SessionID != record.SessionID {
		t.Fatalf("session.start_failed = %+v", failed)
	}
	if tail, _ := failed.Attrs["agent_log_tail"].(string); strings.Contains(tail, "ghp_") || !strings.Contains(tail, "exited 1") {
		t.Fatalf("agent_log_tail = %q", tail)
	}
	stages := 0
	for _, kind := range activity.kinds() {
		if kind == domain.ActivityBootstrapStage {
			stages++
		}
	}
	if stages != 2 {
		t.Fatalf("activity = %v, want both stage reports (agent wait, worker install)", activity.kinds())
	}

	// Every log line of the claim carries the session's identifiers, and the
	// failure is logged once with its stage.
	var sawFailure bool
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %q", raw)
		}
		if line["org_id"] != record.OrgID || line["connection_id"] != "connection-1" || line["sandbox_provider"] != "coder" {
			t.Fatalf("log line missing trace fields: %v", line)
		}
		if line["msg"] == "sandbox startup failed" {
			sawFailure = true
			if line["failed_stage"] != sandbox.StageWorkerInstall {
				t.Fatalf("failure line = %v", line)
			}
		}
	}
	if !sawFailure {
		t.Fatalf("no 'sandbox startup failed' line in:\n%s", logs.String())
	}

	// The same error on the next tick is not re-captured.
	record.StartupErrorCode = sandbox.StartupErrorBootstrapFailed
	_ = reconciler.reconcileClaim(context.Background(), record)
	if len(store.details) != 1 || provider.diagnoses != 1 {
		t.Fatalf("details = %d, diagnoses = %d after a repeat; want one capture", len(store.details), provider.diagnoses)
	}
}

func TestNotReadyAgentWaitStaysOffTheTrail(t *testing.T) {
	store := &detailStore{}
	notReady := fmt.Errorf("coder: workspace agent is not ready: %w", sandbox.ErrWorkspaceNotReady)
	provider := &notReadyProvider{byoProvider: byoProvider{
		lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateRunning}},
		bootstrapErr:      notReady,
	}}
	reconciler := byoReconciler(store, provider)
	activity := &recordedActivity{}
	reconciler.SetActivityRecorder(activity)
	record := coderRecord(t, "connection-1", byoBudget, time.Minute)
	record.OrgID = "6f1c2c8e-8a39-4c55-9a5c-1d6a8c1f0a01"
	_ = reconciler.reconcileClaim(context.Background(), record)
	if _, ok := activity.find(domain.ActivityBootstrapStage); ok {
		t.Fatalf("activity = %v; a not-ready agent wait must not be trailed", activity.kinds())
	}
}

type notReadyProvider struct {
	byoProvider
}

func (p *notReadyProvider) BootstrapWorker(ctx context.Context, id sandbox.ID, bootstrap sandbox.WorkerBootstrap) error {
	err := p.byoProvider.BootstrapWorker(ctx, id, bootstrap)
	sandbox.BeginStage(ctx, sandbox.StageAgentWait)(err, nil)
	return err
}

func TestLifecycleActivityForPauseAndTerminate(t *testing.T) {
	store := &lifecycleStore{}
	provider := &byoProvider{lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateRunning,
	}}}
	reconciler := byoReconciler(store, provider)
	activity := &recordedActivity{}
	reconciler.SetActivityRecorder(activity)

	paused := coderRecord(t, "", nil, time.Minute)
	paused.DesiredState = domain.SandboxDesiredPaused
	if err := reconciler.reconcileSandbox(context.Background(), paused); err != nil {
		t.Fatal(err)
	}
	if _, ok := activity.find(domain.ActivitySandboxPaused); !ok {
		t.Fatalf("activity = %v, want sandbox.paused", activity.kinds())
	}

	parked := coderRecord(t, "connection-1", byoBudget, 21*time.Minute)
	if err := reconciler.reconcileSandbox(context.Background(), parked); err != nil {
		t.Fatal(err)
	}
	terminated, ok := activity.find(domain.ActivitySandboxTerminated)
	if !ok || terminated.Attrs["customer_owned"] != true {
		t.Fatalf("activity = %v (%+v), want sandbox.terminated for customer-owned compute", activity.kinds(), terminated)
	}
}

func TestStartupDetailIsBoundedAndHeadFirst(t *testing.T) {
	huge := strings.Repeat("line of build output\n", 2000)
	detail := startupDetail(&sandbox.StartupError{Code: "bootstrap_failed"}, "worker_install", huge,
		sandbox.Diagnostics{BuildLog: strings.Split(huge, "\n"), AgentLog: strings.Split(huge, "\n")}, errors.New("timeout"))
	if len(detail) > startupDetailLimit || !strings.HasPrefix(detail, "code: bootstrap_failed\nstage: worker_install") {
		t.Fatalf("detail is %d bytes, prefix %q", len(detail), detail[:min(60, len(detail))])
	}
}
