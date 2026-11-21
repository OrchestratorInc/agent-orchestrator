package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func openBreakerAttempt(t *testing.T, svc *Service, cfg ports.SpawnConfig) *autoSpawnAttempt {
	t.Helper()
	var attempt *autoSpawnAttempt
	_, _, _, err := svc.spawnUnlessAutoPaused(t.Context(), cfg, func(ctx context.Context) (domain.Session, int, int, error) {
		attempt = autoSpawnAttemptFrom(ctx)
		return domain.Session{SessionRecord: domain.SessionRecord{ProvisionState: domain.SessionProvisionProvisioning}}, 0, 0, nil
	})
	if err != nil {
		t.Fatalf("admit attempt: %v", err)
	}
	return attempt
}

func TestAsyncScheduledStartFailuresCount(t *testing.T) {
	svc, _, _, _ := newBreakerService(t)
	runID := domain.AutomationRunID("run-1")
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, AutomationRunID: &runID}
	for i := 0; i < autoSpawnFailureLimit; i++ {
		svc.settleAutoSpawn(openBreakerAttempt(t, svc, cfg), false, sessionmanager.ErrUnsupportedModel)
	}
	if _, _, _, err := svc.Spawn(t.Context(), cfg); spawnCode(err) != "AUTO_SPAWN_PAUSED" {
		t.Fatalf("scheduled start = %v", err)
	}
	svc2, _, _, _ := newBreakerService(t)
	for i := 0; i < 5; i++ {
		svc2.settleAutoSpawn(openBreakerAttempt(t, svc2, cfg), false, context.Canceled)
	}
	if _, _, _, err := svc2.Spawn(t.Context(), intakeStart); spawnCode(err) == "AUTO_SPAWN_PAUSED" {
		t.Fatal("canceled starts paused intake")
	}
}

func TestAutoSpawnTransientFailuresNeverPauseAndBreakTheStreak(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, errors.New("disk stall"), sessionmanager.ErrChatController, sessionmanager.ErrWorkspaceCreate} {
		t.Run(failure.Error(), func(t *testing.T) {
			svc, _, fc, attempts := newBreakerService(t)
			fc.spawnErr = sessionmanager.ErrUnsupportedModel
			for i := 0; i < 2; i++ {
				_, _, _, _ = svc.Spawn(t.Context(), intakeStart)
			}
			fc.spawnErr = failure
			for i := 0; i < 5; i++ {
				if _, _, _, err := svc.Spawn(t.Context(), intakeStart); spawnCode(err) == "AUTO_SPAWN_PAUSED" {
					t.Fatal("transient failure paused automatic starts")
				}
			}
			fc.spawnErr = sessionmanager.ErrUnsupportedModel
			for i := 0; i < 2; i++ {
				_, _, _, _ = svc.Spawn(t.Context(), intakeStart)
			}
			if *attempts != 9 {
				t.Fatalf("attempts = %d, want all 9 to run", *attempts)
			}
		})
	}
}

func TestAutoSpawnCooldownAllowsRecoveryWithoutSettingsChange(t *testing.T) {
	svc, st, fc, attempts := newBreakerService(t)
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	svc.clock = func() time.Time { return now }
	fc.spawnErr = ports.ErrChatAuthRequired
	for i := 0; i < 3; i++ {
		_, _, _, _ = svc.Spawn(t.Context(), intakeStart)
	}
	_, _, _, err := svc.Spawn(t.Context(), intakeStart)
	if spawnCode(err) != "AUTO_SPAWN_PAUSED" {
		t.Fatalf("before cooldown = %v", err)
	}
	// Logging in changes neither the project settings nor the harness.
	fc.spawnErr = nil
	now = now.Add(30 * time.Minute)
	if pauses := svc.AutoSpawnPauses(t.Context(), st.projects["mer"]); len(pauses) != 0 {
		t.Fatalf("expired pauses = %+v", pauses)
	}
	if _, _, _, err := svc.Spawn(t.Context(), intakeStart); err != nil {
		t.Fatalf("probe after login = %v", err)
	}
	if *attempts != 4 {
		t.Fatalf("attempts = %d, want 4", *attempts)
	}
	if _, _, _, err := svc.Spawn(t.Context(), intakeStart); err != nil {
		t.Fatalf("after recovered probe = %v", err)
	}
}

func TestAutoSpawnCooldownAdmitsOnlyOneProbeAndRepauses(t *testing.T) {
	svc, _, fc, attempts := newBreakerService(t)
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	svc.clock = func() time.Time { return now }
	fc.spawnErr = sessionmanager.ErrUnsupportedModel
	for i := 0; i < 3; i++ {
		_, _, _, _ = svc.Spawn(t.Context(), intakeStart)
	}
	now = now.Add(30 * time.Minute)
	_, _, _, err := svc.Spawn(t.Context(), intakeStart)
	if spawnCode(err) != "UNSUPPORTED_MODEL" || *attempts != 4 {
		t.Fatalf("probe = %v after %d attempts", err, *attempts)
	}
	for i := 0; i < 5; i++ {
		if _, _, _, err := svc.Spawn(t.Context(), intakeStart); spawnCode(err) != "AUTO_SPAWN_PAUSED" {
			t.Fatalf("after failed probe = %v", err)
		}
	}
	now = now.Add(30 * time.Minute)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if _, paused := svc.autoSpawns.begin(autoSpawnKey{project: "mer", model: "old-model", kind: domain.KindWorker}, autoSpawnSettings(domain.ProjectRecord{Config: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "old-model"}}}), true, now); !paused {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("concurrent probes = %d, want 1", admitted.Load())
	}
	// A slow probe remains the only probe even beyond another cooldown.
	key := autoSpawnKey{project: "mer", model: "old-model", kind: domain.KindWorker}
	settings := autoSpawnSettings(domain.ProjectRecord{Config: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "old-model"}}})
	if _, paused := svc.autoSpawns.begin(key, settings, true, now.Add(time.Hour)); !paused {
		t.Fatal("an in-flight probe admitted another automatic start")
	}
}

func TestAutoSpawnConfigurationsAreIsolated(t *testing.T) {
	svc, st, fc, _ := newBreakerService(t)
	bad := intakeStart
	bad.Harness = domain.HarnessClaudeCode
	bad.AgentConfig.Model = "invalid"
	bad.AgentConfig.Effort = "max"
	fc.spawnErr = sessionmanager.ErrUnsupportedModel
	for i := 0; i < 3; i++ {
		_, _, _, _ = svc.Spawn(t.Context(), bad)
	}
	good := bad
	good.AgentConfig.Model = "working"
	fc.spawnErr = nil
	if _, _, _, err := svc.Spawn(t.Context(), good); err != nil {
		t.Fatal(err)
	}
	manual := good
	manual.CreatedBy = domain.SessionCreatedByUser
	if _, _, _, err := svc.Spawn(t.Context(), manual); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.Spawn(t.Context(), bad); spawnCode(err) != "AUTO_SPAWN_PAUSED" {
		t.Fatalf("bad config lost pause: %v", err)
	}
	otherEffort := bad
	otherEffort.AgentConfig.Effort = "high"
	if _, _, _, err := svc.Spawn(t.Context(), otherEffort); err != nil {
		t.Fatal(err)
	}
	pauses := svc.AutoSpawnPauses(t.Context(), st.projects["mer"])
	if len(pauses) != 1 || pauses[0].Model != "invalid" || pauses[0].Effort != "max" {
		t.Fatalf("pause summaries = %+v", pauses)
	}
}

func TestAutoSpawnStaleAsyncOutcomesCannotAffectNewGeneration(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(fmt.Sprint(ready), func(t *testing.T) {
			svc, st, _, _ := newBreakerService(t)
			old := []*autoSpawnAttempt{openBreakerAttempt(t, svc, intakeStart), openBreakerAttempt(t, svc, intakeStart), openBreakerAttempt(t, svc, intakeStart)}
			project := st.projects["mer"]
			project.Config.AgentConfig.Mode = "repaired"
			st.projects["mer"] = project
			for i := 0; i < 3; i++ {
				svc.settleAutoSpawn(openBreakerAttempt(t, svc, intakeStart), false, sessionmanager.ErrUnsupportedModel)
			}
			for _, attempt := range old {
				svc.settleAutoSpawn(attempt, ready, ports.ErrUnsupportedEffort)
			}
			pauses := svc.AutoSpawnPauses(t.Context(), project)
			if len(pauses) != 1 || pauses[0].Failures != 3 || pauses[0].ErrorCode != "UNSUPPORTED_MODEL" {
				t.Fatalf("stale outcome changed current pause: %+v", pauses)
			}
		})
	}
}

func TestAutomationAttributedAsyncProbeSettlesWithoutRunID(t *testing.T) {
	svc, cmd, _, _, _ := newAsyncSpawnService(t)
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, CreatedBy: domain.SessionCreatedByAutomation}
	for i := 0; i < 3; i++ {
		sess, _, _, err := svc.Spawn(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		cmd.observer(context.Background(), sess.SessionRecord, domain.SessionProvisionFailed, sessionmanager.ErrUnsupportedModel)
	}
	cmd.clock.advance(30 * time.Minute)
	sess, _, _, err := svc.Spawn(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	cmd.observer(context.Background(), sess.SessionRecord, domain.SessionProvisionFailed, sessionmanager.ErrUnsupportedModel)
	cmd.clock.advance(30 * time.Minute)
	if _, _, _, err := svc.Spawn(t.Context(), cfg); err != nil {
		t.Fatalf("failed async probe remained reserved: %v", err)
	}
}

func TestAutoSpawnManualFailuresDoNotRetainNewConfigurations(t *testing.T) {
	svc, _, fc, _ := newBreakerService(t)
	fc.spawnErr = fmt.Errorf("spawn: %w", sessionmanager.ErrUnsupportedModel)
	for i := 0; i < 50; i++ {
		cfg := manualStart
		cfg.AgentConfig.Model = fmt.Sprintf("manual-model-%d", i)
		if _, _, _, err := svc.Spawn(t.Context(), cfg); err == nil {
			t.Fatal("expected manual failure")
		}
	}
	if got := len(svc.autoSpawns.streaks); got != 0 {
		t.Fatalf("retained %d manual configurations, want zero", got)
	}
	for i := 0; i < 3; i++ {
		_, _, _, _ = svc.Spawn(t.Context(), intakeStart)
	}
	if got := len(svc.autoSpawns.streaks); got != 1 {
		t.Fatalf("automatic streaks = %d, want one", got)
	}
	_, _, _, _ = svc.Spawn(t.Context(), manualStart)
	if _, _, _, err := svc.Spawn(t.Context(), intakeStart); spawnCode(err) != "AUTO_SPAWN_PAUSED" {
		t.Fatalf("manual failure changed existing pause: %v", err)
	}
	fc.spawnErr = nil
	if _, _, _, err := svc.Spawn(t.Context(), manualStart); err != nil {
		t.Fatal(err)
	}
	if got := len(svc.autoSpawns.streaks); got != 0 {
		t.Fatalf("manual success retained matching pause: %d", got)
	}
}
