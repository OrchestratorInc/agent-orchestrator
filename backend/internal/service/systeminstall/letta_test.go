package systeminstall

import "testing"

func TestLettaInstallUsesPinnedOfficialPackage(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			p := newTestService(goos, "npm").planAgent(TargetLettaCode)
			if p.Unsupported || p.Method != "npm" || p.Script != nil || len(p.Command) == 0 {
				t.Fatalf("install plan = %#v", p)
			}
			if got := p.Command[len(p.Command)-1]; got != "@letta-ai/letta-code@0.34.9" {
				t.Fatalf("package = %q", got)
			}
		})
	}
}

func TestLettaRequiresSupportedNodeRuntime(t *testing.T) {
	if got := minimumNodeVersionForTarget(TargetLettaCode); got != [3]int{22, 19, 0} {
		t.Fatalf("minimum node = %v", got)
	}
}
