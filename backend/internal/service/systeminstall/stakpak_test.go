package systeminstall

import (
	"context"
	"testing"
)

func TestStakpakUsesOfficialInstallerWithoutHomebrew(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		p := newTestService(goos, "bash").planAgent(TargetStakpak)
		if p.Unsupported || p.Script == nil || p.Script.URL != "https://stakpak.dev/install.sh" {
			t.Fatalf("%s: %+v", goos, p)
		}
	}
}

func TestStakpakWindowsNeedsManualReleaseInstall(t *testing.T) {
	p := newTestService("windows", "bash", "powershell.exe").planAgent(TargetStakpak)
	if !p.Unsupported || p.Method != "manual" {
		t.Fatalf("windows plan = %+v; want manual unsupported", p)
	}
}

// install.sh installs through Homebrew whenever brew is on PATH (its prompt
// defaults to yes when headless), so with Homebrew present it is not offered
// as a second, identical method.
func TestStakpakOffersOnlyHomebrewWhenBrewIsPresent(t *testing.T) {
	s := newTestService("darwin", "brew", "bash")
	plans, err := s.AgentPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.AgentID != "stakpak" {
			continue
		}
		if len(plan.Methods) != 1 || plan.Methods[0].ID != "homebrew" {
			t.Fatalf("stakpak methods = %+v, want Homebrew only", plan.Methods)
		}
		return
	}
	t.Fatal("stakpak install plan is missing")
}
