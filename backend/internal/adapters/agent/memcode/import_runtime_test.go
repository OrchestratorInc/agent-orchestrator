package memcode

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestMemcodeLaunchPreservesSDKRouteWithoutChangingCredentials(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://unrelated.invalid/v1")
	env := map[string]string{"OPENAI_BASE_URL": "https://unrelated.invalid/v1", "OPENAI_API_KEY": "synthetic-key", "MEMCODE_ENDPOINT_URL": "http://127.0.0.1:11434/v1"}
	New().AugmentRuntimeLaunchEnv(env, t.TempDir(), domain.SessionID("ao-1"), "launch-1")
	if env["OPENAI_BASE_URL"] != "https://unrelated.invalid/v1" || env["OPENAI_API_KEY"] != "synthetic-key" || env["MEMCODE_ENDPOINT_URL"] != "http://127.0.0.1:11434/v1" {
		t.Fatal("runtime augmentation changed credentials or custom endpoint")
	}
}

func TestMemcodeNativeCrashRefusesBeforeRestoreAdoption(t *testing.T) {
	p := &restorePlan{}
	for _, output := range []string{
		readyTerminal + "\npanic: runtime error: invalid memory address",
		"github.com/memcode-ai/vaxis/ui.(*actionsElement).Rebuild(0x123)",
		"github.com/memcode-ai/memcode/internal/vxui.Run(0x123)",
		"fatal error: concurrent map writes",
	} {
		if ok, err := p.Ready(context.Background(), "launch-1", output); ok || err == nil || err.Error() != "memcode native terminal crashed during restore" {
			t.Fatal("native crash accepted before resume")
		}
		if ok, err := p.Restored(context.Background(), "launch-1", output); ok || err == nil || err.Error() != "memcode native terminal crashed during restore" {
			t.Fatal("native crash accepted after resume")
		}
	}
	if err := memcodeRestoreCrash("panic: historical discussion\n" + readyTerminal); err != nil {
		t.Fatal("historical transcript panic rejected current idle terminal")
	}
}
