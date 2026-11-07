package systeminstall

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Coder Agents needs the official `coder` CLI for native login. The plans
// follow Coder's documented channels: install.sh as a no-sudo standalone
// install on Linux and macOS (with the Homebrew tap offered on macOS), and
// winget on Windows.
func TestCoderAgentsUsesOfficialCoderInstallers(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		plan := newTestService(goos, "sh", "brew").planAgent(TargetCoderAgents)
		if plan.Unsupported || plan.Script == nil || plan.Script.URL != "https://coder.com/install.sh" {
			t.Fatalf("%s plan = %+v", goos, plan)
		}
		args := strings.Join(plan.Script.Args, " ")
		if !slices.Contains(plan.Script.Args, "--stable") || !strings.Contains(args, "--method standalone") || !strings.Contains(args, "--prefix ") {
			t.Fatalf("%s installer args = %q; want a stable standalone user-prefix install", goos, args)
		}
	}
	planner, err := newTestService("darwin", "sh", "brew").newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hasBrew := false
	for _, plan := range planner.agentMethodPlans(TargetCoderAgents, AgentOperationInstall) {
		hasBrew = hasBrew || (plan.Method == "homebrew" && plan.Package == "coder/coder/coder")
	}
	if !hasBrew {
		t.Fatal("macOS plans do not offer the coder/coder Homebrew tap")
	}
	windows := newTestService("windows", "winget").planAgent(TargetCoderAgents)
	if windows.Unsupported || windows.Method != "winget" || windows.Package != "Coder.Coder" {
		t.Fatalf("windows plan = %+v", windows)
	}
	if plan := newTestService("freebsd", "sh").planAgent(TargetCoderAgents); !plan.Unsupported {
		t.Fatalf("freebsd plan = %+v, want manual", plan)
	}
}
