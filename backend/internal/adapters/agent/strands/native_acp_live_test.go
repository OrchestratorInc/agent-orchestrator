package strands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestNativeACPApprovalAndRestore is deliberately opt-in: it launches the real
// installed CLI and consumes authorized provider usage. The default CI suite
// must neither require credentials nor substitute a fake server for this gate.
// CLI 0.2.0 currently fails the permission-request assertion; leave Chat gated.
func TestNativeACPApprovalAndRestore(t *testing.T) {
	if os.Getenv("AO_STRANDS_ACP_LIVE") != "1" {
		t.Skip("set AO_STRANDS_ACP_LIVE=1 for real native ACP qualification")
	}
	binary := os.Getenv("AO_STRANDS_ACP_BINARY")
	config := os.Getenv("AO_STRANDS_ACP_CONFIG")
	if !filepath.IsAbs(binary) || !filepath.IsAbs(config) {
		t.Fatal("AO_STRANDS_ACP_BINARY and AO_STRANDS_ACP_CONFIG must be explicit absolute paths")
	}
	cmd := exec.CommandContext(t.Context(), "python3", "testdata/native_acp_probe.py",
		"--binary", binary, "--config", config,
		"--runtime-root", filepath.Join(t.TempDir(), "native-acp"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native ACP qualification failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
