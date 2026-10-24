package systeminstall

import (
	"context"
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

func TestMiniMaxUpdateRerunsOfficialInstaller(t *testing.T) {
	if !installerUpdateTargets[TargetMiniMaxCode] {
		t.Fatal("MiniMax official-installer updates must re-run the installer")
	}
	if got := minimumNodeVersionForTarget(TargetMiniMaxCode); got != [3]int{22, 19, 0} {
		t.Fatalf("minimum Node = %v, want 22.19.0", got)
	}
}
