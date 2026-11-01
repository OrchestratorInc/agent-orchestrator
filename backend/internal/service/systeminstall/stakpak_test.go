package systeminstall

import (
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
