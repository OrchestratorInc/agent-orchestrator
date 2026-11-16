package claudeacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const crashingNodeScript = "#!/bin/sh\necho '# Fatal error in , line 0' >&2\necho '# Check failed: 12 == (*__error()).' >&2\nexit 133\n"
const healthyNodeScript = "#!/bin/sh\necho ok\n"

// withIntelMac points the repair seams at a fake Intel Mac and returns the
// repair root and a counter of codesign calls.
func withIntelMac(t *testing.T, codesign func(context.Context, string, string) error) (string, *int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake Node is a shell script")
	}
	root := filepath.Join(t.TempDir(), "node-jit")
	calls := 0
	oldOS, oldArch, oldSign, oldRoot := jitRepairGOOS, jitRepairGOARCH, codesignNode, jitRepairRoot
	jitRepairGOOS, jitRepairGOARCH = "darwin", "amd64"
	codesignNode = func(ctx context.Context, node, entitlements string) error {
		calls++
		return codesign(ctx, node, entitlements)
	}
	jitRepairRoot = func() (string, error) { return root, nil }
	jitNodesMu.Lock()
	jitNodes = map[string]string{}
	jitNodesMu.Unlock()
	t.Cleanup(func() {
		jitRepairGOOS, jitRepairGOARCH, codesignNode, jitRepairRoot = oldOS, oldArch, oldSign, oldRoot
		jitNodesMu.Lock()
		jitNodes = map[string]string{}
		jitNodesMu.Unlock()
	})
	return root, &calls
}

func writeFakeNode(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // Test executable.
		t.Fatal(err)
	}
	return path
}

// fixingCodesign stands in for a codesign that adds the missing entitlement.
func fixingCodesign(_ context.Context, node, entitlements string) error {
	plist, err := os.ReadFile(entitlements) //nolint:gosec // Test path.
	if err != nil {
		return err
	}
	if !strings.Contains(string(plist), "com.apple.security.cs.allow-unsigned-executable-memory") {
		return errors.New("entitlements lack allow-unsigned-executable-memory")
	}
	return os.WriteFile(node, []byte(healthyNodeScript), 0o755) //nolint:gosec // Test executable.
}

func TestJITCapableNodeKeepsHealthyNode(t *testing.T) {
	_, calls := withIntelMac(t, fixingCodesign)
	node := writeFakeNode(t, healthyNodeScript)
	got, err := jitCapableNode(context.Background(), node)
	if err != nil || got != node {
		t.Fatalf("jitCapableNode = %q, %v; want %q", got, err, node)
	}
	if *calls != 0 {
		t.Fatalf("codesign ran %d times for a healthy Node", *calls)
	}
}

func TestJITCapableNodeSkipsOtherPlatforms(t *testing.T) {
	withIntelMac(t, fixingCodesign)
	jitRepairGOARCH = "arm64"
	node := filepath.Join(t.TempDir(), "missing-node")
	got, err := jitCapableNode(context.Background(), node)
	if err != nil || got != node {
		t.Fatalf("jitCapableNode = %q, %v; want untouched %q", got, err, node)
	}
}

func TestJITCapableNodeRepairsEntitlementCrash(t *testing.T) {
	root, calls := withIntelMac(t, fixingCodesign)
	node := writeFakeNode(t, crashingNodeScript)
	got, err := jitCapableNode(context.Background(), node)
	if err != nil {
		t.Fatalf("jitCapableNode: %v", err)
	}
	if got == node || !strings.HasPrefix(got, root+string(filepath.Separator)) {
		t.Fatalf("jitCapableNode = %q, want a copy under %q", got, root)
	}
	if source, _ := os.ReadFile(node); string(source) != crashingNodeScript { //nolint:gosec // Test path.
		t.Fatal("repair modified the packaged Node")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("repair root holds %d entries, want only the installed copy", len(entries))
	}

	// Cached for the daemon's lifetime.
	again, err := jitCapableNode(context.Background(), node)
	if err != nil || again != got || *calls != 1 {
		t.Fatalf("second call = %q, %v with %d codesign calls; want cached %q", again, err, *calls, got)
	}

	// A new daemon reuses the installed copy without re-signing.
	jitNodesMu.Lock()
	jitNodes = map[string]string{}
	jitNodesMu.Unlock()
	reused, err := jitCapableNode(context.Background(), node)
	if err != nil || reused != got || *calls != 1 {
		t.Fatalf("restart = %q, %v with %d codesign calls; want reused %q", reused, err, *calls, got)
	}
}

func TestJITCapableNodeReportsFailedRepair(t *testing.T) {
	withIntelMac(t, func(context.Context, string, string) error { return nil })
	node := writeFakeNode(t, crashingNodeScript)
	_, err := jitCapableNode(context.Background(), node)
	if err == nil {
		t.Fatal("jitCapableNode succeeded although the re-signed copy still crashes")
	}
	for _, want := range []string{"allow-unsigned-executable-memory", "Terminal mode", "does not run JavaScript"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestJITCapableNodeLeavesUnrelatedFailures(t *testing.T) {
	_, calls := withIntelMac(t, fixingCodesign)
	node := writeFakeNode(t, "#!/bin/sh\necho boom >&2\nexit 1\n")
	got, err := jitCapableNode(context.Background(), node)
	if err != nil || got != node || *calls != 0 {
		t.Fatalf("jitCapableNode = %q, %v with %d codesign calls; want bundled Node unchanged", got, err, *calls)
	}
}

const failingNodeScript = "#!/bin/sh\necho 'cannot start' >&2\nexit 1\n"

func TestJITCapableNodeRejectsRepairThatFailsOtherwise(t *testing.T) {
	root, _ := withIntelMac(t, func(_ context.Context, node, _ string) error {
		return os.WriteFile(node, []byte(failingNodeScript), 0o755) //nolint:gosec // Test executable.
	})
	node := writeFakeNode(t, crashingNodeScript)
	_, err := jitCapableNode(context.Background(), node)
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("jitCapableNode error = %v, want the re-signed copy's exit 1 rejected", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("repair root holds %d entries, want the unusable copy discarded", len(entries))
	}
}

func TestJITCapableNodeRebuildsUnusableInstalledCopy(t *testing.T) {
	_, calls := withIntelMac(t, fixingCodesign)
	node := writeFakeNode(t, crashingNodeScript)
	installed, err := jitCapableNode(context.Background(), node)
	if err != nil {
		t.Fatalf("jitCapableNode: %v", err)
	}
	if err := os.WriteFile(installed, []byte(failingNodeScript), 0o755); err != nil { //nolint:gosec // Test executable.
		t.Fatal(err)
	}
	jitNodesMu.Lock()
	jitNodes = map[string]string{}
	jitNodesMu.Unlock()

	rebuilt, err := jitCapableNode(context.Background(), node)
	if err != nil || rebuilt != installed || *calls != 2 {
		t.Fatalf("restart = %q, %v with %d codesign calls; want %q rebuilt", rebuilt, err, *calls, installed)
	}
	if content, _ := os.ReadFile(rebuilt); string(content) != healthyNodeScript { //nolint:gosec // Test path.
		t.Fatalf("installed copy was not rebuilt: %q", content)
	}
}

func TestJITCapableNodeRetriesInconclusiveSmokeTest(t *testing.T) {
	_, calls := withIntelMac(t, fixingCodesign)
	oldSmoke := runNodeSmoke
	t.Cleanup(func() { runNodeSmoke = oldSmoke })
	timedOut := false
	runNodeSmoke = func(ctx context.Context, node string) (int, string, error) {
		if !timedOut {
			timedOut = true
			return 0, "", context.DeadlineExceeded
		}
		return oldSmoke(ctx, node)
	}
	node := writeFakeNode(t, crashingNodeScript)

	first, err := jitCapableNode(context.Background(), node)
	if err != nil || first != node || *calls != 0 {
		t.Fatalf("timed-out check = %q, %v with %d codesign calls; want the bundled Node for now", first, err, *calls)
	}
	second, err := jitCapableNode(context.Background(), node)
	if err != nil || second == node || *calls != 1 {
		t.Fatalf("retry = %q, %v with %d codesign calls; want a repaired copy", second, err, *calls)
	}
}

func TestJITCapableNodeRetriesFailedRepair(t *testing.T) {
	fail := true
	_, calls := withIntelMac(t, func(ctx context.Context, node, entitlements string) error {
		if fail {
			return errors.New("codesign unavailable")
		}
		return fixingCodesign(ctx, node, entitlements)
	})
	node := writeFakeNode(t, crashingNodeScript)
	if _, err := jitCapableNode(context.Background(), node); err == nil {
		t.Fatal("jitCapableNode succeeded although codesign failed")
	}
	fail = false
	repaired, err := jitCapableNode(context.Background(), node)
	if err != nil || repaired == node || *calls != 2 {
		t.Fatalf("retry = %q, %v with %d codesign calls; want the repair retried", repaired, err, *calls)
	}
}
