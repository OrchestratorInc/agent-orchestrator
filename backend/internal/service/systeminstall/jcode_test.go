package systeminstall

import "testing"

// JCode ships one official installer per platform family; both verify the
// release archive's SHA-256 before installing.
func TestJCodeUsesOfficialInstallers(t *testing.T) {
	for goos, want := range map[string]string{
		"linux":   "https://jcode.sh/install",
		"darwin":  "https://jcode.sh/install",
		"windows": "https://jcode.sh/install.ps1",
	} {
		p := newTestService(goos, "bash", "curl", "powershell").planAgent(TargetJCode)
		if p.Unsupported || p.Script == nil || p.Script.URL != want {
			t.Fatalf("%s plan = %+v, want official installer %s", goos, p, want)
		}
		if p.DocsURL != "https://github.com/1jehuang/jcode" {
			t.Fatalf("%s docs = %q", goos, p.DocsURL)
		}
	}
	if source, ok := officialSourceFor(TargetJCode, "linux", "amd64"); !ok || source.ref != "1jehuang/jcode" {
		t.Fatalf("official version source = %+v, %v", source, ok)
	}
}
