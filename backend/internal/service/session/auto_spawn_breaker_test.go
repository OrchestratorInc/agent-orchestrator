package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func newBreakerService(t *testing.T) (*Service, *fakeStore, *fakeCommander, *int) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "old-model"}}}
	attempts := 0
	fc := &fakeCommander{spawnHook: func(context.Context) { attempts++ }}
	return NewWithDeps(Deps{Manager: fc, Store: st}), st, fc, &attempts
}

var (
	intakeStart = ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, CreatedBy: domain.SessionCreatedByAutomation}
	manualStart = ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}
)

func spawnCode(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func TestAutoSpawnStopsAfterThreeIdenticalFailures(t *testing.T) {
	svc, _, fc, attempts := newBreakerService(t)
	fc.spawnErr = fmt.Errorf("spawn: %w", sessionmanager.ErrUnsupportedModel)
	for i := 0; i < autoSpawnFailureLimit; i++ {
		if _, _, _, err := svc.Spawn(context.Background(), intakeStart); spawnCode(err) == "AUTO_SPAWN_PAUSED" {
			t.Fatalf("attempt %d was refused before the limit", i+1)
		}
	}
	_, _, _, err := svc.Spawn(context.Background(), intakeStart)
	if spawnCode(err) != "AUTO_SPAWN_PAUSED" || *attempts != autoSpawnFailureLimit {
		t.Fatalf("fourth automatic start = %v after %d attempts, want AUTO_SPAWN_PAUSED without a new attempt", err, *attempts)
	}
	// Schedules are automatic too.
	runID := domain.AutomationRunID("run-1")
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, AutomationRunID: &runID}); spawnCode(err) != "AUTO_SPAWN_PAUSED" {
		t.Fatalf("scheduled start = %v, want AUTO_SPAWN_PAUSED", err)
	}
	pauses := svc.AutoSpawnPauses(context.Background(), domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "old-model"}}})
	if len(pauses) != 1 || pauses[0].ErrorCode != "UNSUPPORTED_MODEL" || pauses[0].Failures != autoSpawnFailureLimit {
		t.Fatalf("pauses = %+v, want one UNSUPPORTED_MODEL pause after %d failures", pauses, autoSpawnFailureLimit)
	}

	// A manual start is never paused, and its success re-arms automatic starts.
	fc.spawnErr = nil
	if _, _, _, err := svc.Spawn(context.Background(), manualStart); err != nil {
		t.Fatalf("manual start = %v, want it to run while automatic starts are paused", err)
	}
	if _, _, _, err := svc.Spawn(context.Background(), intakeStart); err != nil {
		t.Fatalf("automatic start after a success = %v, want it to run", err)
	}
}

func TestAutoSpawnCountsOnlyIdenticalConsecutiveFailures(t *testing.T) {
	svc, _, fc, _ := newBreakerService(t)
	for _, stage := range []error{sessionmanager.ErrUnsupportedModel, sessionmanager.ErrUnsupportedModel, sessionmanager.ErrWorkspaceCreate, sessionmanager.ErrWorkspaceCreate} {
		fc.spawnErr = fmt.Errorf("spawn: %w", stage)
		if _, _, _, err := svc.Spawn(context.Background(), intakeStart); spawnCode(err) == "AUTO_SPAWN_PAUSED" {
			t.Fatal("two different failure codes tripped the breaker")
		}
	}
	// Manual failures do not count either.
	svc2, _, fc2, _ := newBreakerService(t)
	fc2.spawnErr = fmt.Errorf("spawn: %w", sessionmanager.ErrUnsupportedModel)
	for i := 0; i < 5; i++ {
		_, _, _, _ = svc2.Spawn(context.Background(), manualStart)
	}
	if _, _, _, err := svc2.Spawn(context.Background(), intakeStart); spawnCode(err) == "AUTO_SPAWN_PAUSED" {
		t.Fatal("manual failures paused automatic starts")
	}
}

func TestAutoSpawnResumesWhenProjectAgentSettingsChange(t *testing.T) {
	svc, st, fc, attempts := newBreakerService(t)
	fc.spawnErr = fmt.Errorf("spawn: %w", sessionmanager.ErrUnsupportedModel)
	for i := 0; i <= autoSpawnFailureLimit; i++ {
		_, _, _, _ = svc.Spawn(context.Background(), intakeStart)
	}
	before := *attempts
	project := st.projects["mer"]
	project.Config.AgentConfig.Model = "new-model"
	st.projects["mer"] = project
	fc.spawnErr = nil
	if _, _, _, err := svc.Spawn(context.Background(), intakeStart); err != nil || *attempts != before+1 {
		t.Fatalf("automatic start after a model change = %v, want a new attempt", err)
	}
}

func TestAsyncScheduledStartFailuresCount(t *testing.T) {
	svc, _, _, _ := newBreakerService(t)
	runID := domain.AutomationRunID("run-1")
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, AutomationRunID: &runID}
	for i := 0; i < autoSpawnFailureLimit; i++ {
		svc.settleAutoSpawn(rec, false, fmt.Errorf("spawn: %w", sessionmanager.ErrChatController))
	}
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, AutomationRunID: &runID}); spawnCode(err) != "AUTO_SPAWN_PAUSED" {
		t.Fatalf("start after three failed async schedule starts = %v, want AUTO_SPAWN_PAUSED", err)
	}
	// A cancelled background start is not a failure.
	svc2, _, _, _ := newBreakerService(t)
	for i := 0; i < 5; i++ {
		svc2.settleAutoSpawn(rec, false, fmt.Errorf("spawn: %w", context.Canceled))
	}
	if _, _, _, err := svc2.Spawn(context.Background(), intakeStart); spawnCode(err) == "AUTO_SPAWN_PAUSED" {
		t.Fatal("cancelled starts paused automatic starts")
	}
}
