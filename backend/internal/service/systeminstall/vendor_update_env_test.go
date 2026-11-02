package systeminstall

import (
	"context"
	"io"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type recordedInstallCommands struct{ ran []ports.InstallCommand }

func (r *recordedInstallCommands) RunInstall(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
	r.ran = append(r.ran, command)
	return nil
}

// `droid update` exits 0 without updating when Droid's own auto-update switch
// is off, so the update the user asked for turns it back on.
func TestDroidUpdateTurnsDroidsAutoUpdateBackOn(t *testing.T) {
	s := newTestService("darwin")
	recorder := &recordedInstallCommands{}
	s.installCommands = recorder
	plan := Plan{Target: TargetDroid, Method: "official-installer", Command: []string{"/Users/me/.local/bin/droid", "update"}}
	job := &Job{Target: TargetDroid, Method: "official-installer", Status: StatusInstalling}
	s.runAgentOperation(context.Background(), plan, AgentOperationUpdate, job, nil)
	if len(recorder.ran) != 1 || !slices.Contains(recorder.ran[0].Env, "FACTORY_DROID_AUTO_UPDATE_ENABLED=true") {
		t.Fatalf("droid update ran %+v, want FACTORY_DROID_AUTO_UPDATE_ENABLED=true", recorder.ran)
	}
}
