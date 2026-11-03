package claudeacp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestJITCapableNodeRepairsRealIntelNode reproduces #6621 on real Intel
// hardware: it signs a copy of Node the way the broken release did (hardened
// runtime with allow-jit but without allow-unsigned-executable-memory),
// checks that V8 really crashes, and checks that the repair yields a Node that
// runs JIT-compiled code. It needs an Intel Mac that enforces the entitlement
// (the reports are on macOS 15) and node on PATH; elsewhere it skips.
func TestJITCapableNodeRepairsRealIntelNode(t *testing.T) {
	source, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	broken := filepath.Join(dir, "node")
	if err := copyExecutable(resolved, broken); err != nil {
		t.Fatal(err)
	}
	brokenEntitlements := filepath.Join(dir, "broken.entitlements")
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>com.apple.security.cs.allow-jit</key><true/></dict></plist>
`
	if err := os.WriteFile(brokenEntitlements, []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := defaultCodesignNode(context.Background(), broken, brokenEntitlements); err != nil {
		t.Fatalf("sign broken Node: %v", err)
	}

	ctx := context.Background()
	crashed, err := nodeCrashesOnJIT(ctx, broken)
	if err != nil {
		t.Fatalf("smoke test broken Node: %v", err)
	}
	if !crashed {
		// Not every Intel Mac enforces it: macOS 26 runners run this Node
		// fine, while the #6621 reports are on macOS 15. Without the crash
		// there is nothing to repair, and jitCapableNode keeps the Node.
		t.Skip("Node signed without allow-unsigned-executable-memory did not crash on this Mac; #6621 does not reproduce here")
	}

	root := filepath.Join(t.TempDir(), "node-jit")
	oldRoot := jitRepairRoot
	jitRepairRoot = func() (string, error) { return root, nil }
	jitNodesMu.Lock()
	jitNodes = map[string]jitNodeResult{}
	jitNodesMu.Unlock()
	t.Cleanup(func() {
		jitRepairRoot = oldRoot
		jitNodesMu.Lock()
		jitNodes = map[string]jitNodeResult{}
		jitNodesMu.Unlock()
	})

	repaired, err := jitCapableNode(ctx, broken)
	if err != nil {
		t.Fatalf("jitCapableNode: %v", err)
	}
	if !strings.HasPrefix(repaired, root+string(filepath.Separator)) {
		t.Fatalf("jitCapableNode = %q, want a re-signed copy under %q", repaired, root)
	}
	out, err := exec.Command(repaired, "-e", jitSmokeScript).CombinedOutput() //nolint:gosec // Test executable.
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("re-signed Node: %v: %s", err, out)
	}
	entitlements, err := exec.Command("/usr/bin/codesign", "-d", "--entitlements", "-", repaired).CombinedOutput()
	if err != nil {
		t.Fatalf("codesign -d: %v: %s", err, entitlements)
	}
	if !strings.Contains(string(entitlements), "com.apple.security.cs.allow-unsigned-executable-memory") {
		t.Fatalf("re-signed Node lacks allow-unsigned-executable-memory:\n%s", entitlements)
	}
}
