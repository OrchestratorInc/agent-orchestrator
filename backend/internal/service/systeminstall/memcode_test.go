package systeminstall

import (
	"testing"
)

func TestMemcodeUsesOfficialNativeInstaller(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		p := newTestService(goos, "sh").planAgent(TargetMemcode)
		if p.Unsupported || p.Script == nil || p.Script.URL != "https://memcode.ai/install.sh" {
			t.Fatal(p)
		}
	}
}
