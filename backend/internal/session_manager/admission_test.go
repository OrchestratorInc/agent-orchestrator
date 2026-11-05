package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 删除 Manager 任一路径的 CreateAdmittedSession 会留下无 SessionID 的预留，或使重放重复启动。
func TestM3ManagerEverySpawnPathConsumesOneAdmission(t *testing.T) {
	for _, path := range []string{"ordinary", "client_request", "automation", "prepared"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			s := sqlitetest.MustOpen(t)
			project := domain.ProjectRecord{ID: "admitted", Path: t.TempDir(), RegisteredAt: now, Config: testRoleAgents()}
			if err := s.UpsertProject(ctx, project); err != nil {
				t.Fatal(err)
			}
			runtime := &fakeRuntime{}
			workspace := &fakeWorkspace{path: t.TempDir()}
			m := newAdmittedTestManager(t, Deps{Store: s, Runtime: runtime, Workspace: workspace, Agents: fakeAgents{}, Messenger: &fakeMessenger{}, Lifecycle: lifecycle.New(s, nil), DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil }})
			m.runBackground = func(work func()) { work() }
			cfg := ports.SpawnConfig{ProjectID: "admitted", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "fake local work"}
			switch path {
			case "client_request":
				cfg.ClientRequestID = "request-1"
				cfg.ClientRequestHash = "v1:client-fingerprint"
			case "automation":
				if _, err := s.CreateAutomation(ctx, domain.Automation{ID: "automation-1", ProjectID: "admitted", DisplayName: "fixture", Prompt: "fake work", Kind: domain.KindWorker, RRuleText: "FREQ=DAILY", Timezone: "UTC", Enabled: true, NextRunAt: now, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				runID := domain.AutomationRunID("run-1")
				if _, _, err := s.CreateAutomationRun(ctx, domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunSpawning, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				cfg.AutomationRunID = &runID
			case "prepared":
				token, err := m.PrepareTaskWorkspace(ctx, project)
				if err != nil {
					t.Fatal(err)
				}
				cfg.TaskPreparation = token
			}
			rec, _, _, err := m.Spawn(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			ads, err := s.ListSchedulerAdmissions(ctx, true)
			if err != nil || len(ads) != 1 || ads[0].SessionID != rec.ID || ads[0].State != domain.AdmissionActive {
				t.Fatalf("admission not consumed: %+v %v", ads, err)
			}
			if path == "prepared" && string(rec.ID) != string(cfg.TaskPreparation) {
				t.Fatal("prepared path created another session")
			}
			if path == "client_request" || path == "automation" {
				replay, _, _, err := m.Spawn(ctx, cfg)
				if err != nil || replay.ID != rec.ID || runtime.created != 1 {
					t.Fatalf("duplicate execution: %s creates=%d err=%v", replay.ID, runtime.created, err)
				}
			}
			if path == "ordinary" {
				rec, _, err = s.GetSession(ctx, rec.ID)
				if err != nil {
					t.Fatal(err)
				}
				oldVersion := rec.UpdatedAt
				rec.IsTerminated = true
				rec.UpdatedAt = time.Now().UTC().Add(time.Millisecond)
				if err := s.UpdateSession(ctx, rec); err != nil {
					t.Fatal(err)
				}
				ob := ports.SessionObservation{SessionID: rec.ID, Terminated: true, ObservedAt: rec.UpdatedAt, SessionUpdatedAt: oldVersion}
				if err := s.ReleaseExecutionAdmission(ctx, ads[0], ob, rec.UpdatedAt); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
					t.Fatalf("old terminal facts released slot: %v", err)
				}
				ob.SessionUpdatedAt = rec.UpdatedAt
				if err := s.ReleaseExecutionAdmission(ctx, ads[0], ob, rec.UpdatedAt); err != nil {
					t.Fatal(err)
				}
				// 删除 Restore 的重新准入会使第二次 runtime 创建没有新执行占位。
				restored, err := m.RestoreWithMode(ctx, rec.ID)
				if err != nil {
					t.Fatalf("restore released session: %v", err)
				}
				all, err := s.ListSchedulerAdmissions(ctx, false)
				if err != nil || len(all) != 2 || runtime.created != 2 {
					t.Fatalf("restart admissions=%+v creates=%d err=%v", all, runtime.created, err)
				}
				occupied, _ := s.ListSchedulerAdmissions(ctx, true)
				if len(occupied) != 1 || occupied[0].ID == ads[0].ID || occupied[0].ControllerGeneration != domain.SessionExecutionGeneration(restored.Session) {
					t.Fatalf("restore did not own a new generation: %+v", occupied)
				}
				if err := s.ReleaseExecutionAdmission(ctx, occupied[0], ob, rec.UpdatedAt); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
					t.Fatalf("old termination released restored execution: %v", err)
				}
			}
		})
	}
}

// staleResumePermitStore 将真实恢复入口的写栅栏固定为旧任期拒绝，运行时不得被调用。
type staleResumePermitStore struct{ *workflowBindingFakeStore }

func (*staleResumePermitStore) ValidateExecutionPermit(context.Context, domain.ExecutionPermit, time.Time) error {
	return domain.ErrSchedulerFence
}

// 删除 workflowSpawnContext 的许可事务校验会重新启动已经失去任期的 Controller。
func TestM3WorkflowResumeRefusesStaleExecutionPermit(t *testing.T) {
	launcher := &recordingLauncher{}
	m, store := newWorkflowResumeManager(t, launcher)
	until := time.Now().UTC().Add(time.Minute)
	boundWorkflowSession(store, domain.WorkflowBindingStateBoundReady, "local-lease", &until)
	m.store = &staleResumePermitStore{store}
	ctx := ports.WithExecutionPermit(context.Background(), domain.ExecutionPermit{AttemptID: store.attempt.ID, SessionID: "mer-1"})
	if _, err := m.ResumeAgentWithMode(ctx, "mer-1"); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("stale resume accepted: %v", err)
	}
	if len(launcher.started) != 0 {
		t.Fatal("stale resume started a controller")
	}
}
