package systeminstall

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// MiniMax offers its official installer on every OS and npm as an alternative;
// both install the latest release.
func TestMiniMaxOffersOfficialInstallerThenNPM(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			s := newTestService(goos, "bash", "powershell.exe", "npm")
			planner, err := s.newRequestPlanner(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			plans := planner.agentMethodPlans(TargetMiniMaxCode, AgentOperationInstall)
			if len(plans) != 2 {
				t.Fatalf("plans = %+v", plans)
			}
			if plans[0].Method != "official-installer" || plans[0].Script == nil || plans[0].DocsURL != "https://agent.minimax.io/docs/cli/quick-start" {
				t.Fatalf("official plan = %+v", plans[0])
			}
			if plans[1].Method != "npm" || plans[1].Package != "@minimax-ai/code" || !slices.Contains(plans[1].Command, "@minimax-ai/code") {
				t.Fatalf("npm plan = %+v", plans[1])
			}
		})
	}
}

// Only Kimi's installer re-run is pinned to the approved release and bound to
// the install it updates. An official-installer MiniMax install therefore gets
// manual-update guidance, not an Update that would install an unpinned latest
// without binding it to the copy sessions run.
func TestMiniMaxOfficialInstallUpdatesManually(t *testing.T) {
	if installerUpdateTargets[TargetMiniMaxCode] {
		t.Fatal("MiniMax has no pinned, install-bound installer re-run")
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			s := newTestService(goos, "bash", "powershell.exe", "npm")
			planner, err := s.newRequestPlanner(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var official *Plan
			for _, plan := range planner.agentMethodPlans(TargetMiniMaxCode, AgentOperationUpdate) {
				if plan.Method == "official-installer" {
					official = &plan
				}
			}
			if official == nil || !official.Unsupported || official.Script != nil {
				t.Fatalf("official-installer update = %+v, want unsupported with no installer re-run", official)
			}
			home := t.TempDir()
			binary := filepath.Join(home, ".minimax-code", "bin", "mcode")
			if goos == "windows" {
				binary = filepath.Join(home, ".minimax-code", "mcode.cmd")
			}
			if vendorMaintainable(goos, TargetMiniMaxCode, binary, home) {
				t.Fatalf("update advisory offered a vendor update for %s", binary)
			}
		})
	}
}

func TestMiniMaxMinimumNode(t *testing.T) {
	if got := minimumNodeVersionForTarget(TargetMiniMaxCode); got != [3]int{22, 19, 0} {
		t.Fatalf("minimum Node = %v, want 22.19.0", got)
	}
}
