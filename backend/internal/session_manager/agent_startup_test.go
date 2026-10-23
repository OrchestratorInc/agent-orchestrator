package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type initializingAgent struct {
	*recordingAgent
	initialized     []ports.LaunchConfig
	initializeErr   error
	startupHeld     bool
	startupAcquired int
	startupReleased int
}

func (a *initializingAgent) BeginStartup(context.Context, ports.LaunchConfig) (func(), error) {
	a.startupHeld = true
	a.startupAcquired++
	return func() { a.startupHeld = false; a.startupReleased++ }, nil
}

func (a *initializingAgent) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if !a.startupHeld {
		return nil, errors.New("launch command ran without startup guard")
	}
	return a.recordingAgent.GetLaunchCommand(ctx, cfg)
}

func (a *initializingAgent) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if !a.startupHeld {
		return nil, false, errors.New("restore command ran without startup guard")
	}
	return a.recordingAgent.GetRestoreCommand(ctx, cfg)
}

func (a *initializingAgent) InitializeTerminal(_ context.Context, cfg ports.LaunchConfig, _ ports.AgentStartupTerminal) error {
	if !a.startupHeld {
		return errors.New("initializer ran without startup guard")
	}
	a.initialized = append(a.initialized, cfg)
	return a.initializeErr
}

func TestNativeInitializationRunsOnSpawnAndNativeRestore(t *testing.T) {
	m, st, _, _ := newManager()
	a := &initializingAgent{recordingAgent: &recordingAgent{}}
	m.agents = singleAgent{agent: a}
	project := st.projects["mer"]
	project.Config.Env = map[string]string{"FREEBUFF_CONFIG_DIR": t.TempDir()}
	st.projects["mer"] = project
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.initialized) != 1 {
		t.Fatalf("initializer calls=%d", len(a.initialized))
	}
	want := project.Config.Env["FREEBUFF_CONFIG_DIR"]
	if a.lastLaunch.Env["FREEBUFF_CONFIG_DIR"] != want || a.initialized[0].Env["FREEBUFF_CONFIG_DIR"] != want {
		t.Fatal("spawn omitted effective native environment")
	}
	rec.Metadata.AgentSessionID = "native-existing"
	rec.Activity.State = domain.ActivityExited
	rec.IsTerminated = true
	st.sessions[rec.ID] = rec
	if _, err := m.RestoreWithMode(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if a.startupHeld || a.startupAcquired != 2 || a.startupReleased != 2 {
		t.Fatalf("startup guard acquired=%d released=%d held=%v", a.startupAcquired, a.startupReleased, a.startupHeld)
	}
	if len(a.initialized) != 2 {
		t.Fatalf("native restore initializer calls=%d", len(a.initialized))
	}
	if a.lastRestore.Env["FREEBUFF_CONFIG_DIR"] != want || a.initialized[1].Env["FREEBUFF_CONFIG_DIR"] != want {
		t.Fatal("restore omitted effective native environment")
	}
}

func TestNativeInitializationFailureDoesNotReportSpawnSuccess(t *testing.T) {
	m, _, rt, _ := newManager()
	a := &initializingAgent{recordingAgent: &recordingAgent{}, initializeErr: errors.New("native model selection not confirmed")}
	m.agents = singleAgent{agent: a}
	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI})
	if err == nil || !strings.Contains(err.Error(), "native model selection not confirmed") {
		t.Fatalf("spawn err=%v", err)
	}
	if a.startupHeld || a.startupAcquired != 1 || a.startupReleased != 1 {
		t.Fatal("failed initializer leaked startup guard")
	}
	if rt.destroyed == 0 {
		t.Fatal("failed initializer left new runtime running")
	}
}
