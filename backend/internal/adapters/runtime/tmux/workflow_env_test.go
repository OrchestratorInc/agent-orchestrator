package tmux

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestTmuxChildDropsLegacyWorkflowLease 删除 runner 或最终 shell 清理会泄露 daemon/旧 server 环境。
func TestTmuxChildDropsLegacyWorkflowLease(t *testing.T) {
	t.Setenv("AO_WORKFLOW_LEASE", "fake-legacy-tmux")
	t.Setenv("AO_TEST_WORKFLOW_LEASE_PROBE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := (execRunner{}).Run(ctx, []string{"aO_wOrKfLoW_lEaSe=fake-overlay"}, exe, "-test.run=^TestTmuxWorkflowLeaseProbe$"); err != nil {
		t.Fatalf("runner lease probe: %v", err)
	}
	cfg := ports.RuntimeConfig{
		WorkspacePath: t.TempDir(), ExitOnCommandCompletion: true,
		Argv: []string{exe, "-test.run=^TestTmuxWorkflowLeaseProbe$"},
		Env:  map[string]string{"AO_WORKFLOW_LEASE": "fake-configured", "ao_workflow_lease": "fake-alias"},
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", buildLaunchCommand(cfg))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shell lease probe: %v: %s", err, out)
	}
	for _, entry := range attachEnv([]string{"AO_WORKFLOW_LEASE=fake-attach", "ao_workflow_lease=fake-alias"}) {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "AO_WORKFLOW_LEASE") {
			t.Fatal("attach client retained the legacy lease")
		}
	}
}

// TestTmuxWorkflowLeaseProbe 是真实执行的假 Agent，只检查测试指定的租约名。
func TestTmuxWorkflowLeaseProbe(t *testing.T) {
	if os.Getenv("AO_TEST_WORKFLOW_LEASE_PROBE") != "1" {
		return
	}
	for _, key := range []string{"AO_WORKFLOW_LEASE", "ao_workflow_lease", "aO_wOrKfLoW_lEaSe"} {
		if _, present := os.LookupEnv(key); present {
			t.Fatal("child inherited the legacy workflow lease")
		}
	}
}
