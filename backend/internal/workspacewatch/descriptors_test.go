package workspacewatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// openDescriptors counts this process's open file descriptors. Readdirnames
// avoids stat'ing each entry, which trips over descriptors closed mid-scan.
func openDescriptors(t *testing.T) (int, bool) {
	t.Helper()
	dir, err := os.Open("/dev/fd")
	if err != nil {
		return 0, false
	}
	defer func() { _ = dir.Close() }()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return 0, false
	}
	return len(names), true
}

// TestWatchReleasesDescriptorsOnCancel pins the descriptor accounting of a
// cancelled watch. The kqueue backend keeps one descriptor per watched file, so
// a workspace watcher that does not release them on teardown costs roughly two
// thousand descriptors per stream against a 61,440 per-process cap on macOS:
// a handful of workspace views wedge the whole daemon, and every later
// operation that needs a descriptor (spawning git, opening the next watcher)
// fails with a bare 500. fsnotify v1.9.0 leaked exactly this way, because
// kqueue.Close marked the watcher closed before its removal loop ran, which
// made every removal a no-op.
func TestWatchReleasesDescriptorsOnCancel(t *testing.T) {
	baseline, ok := openDescriptors(t)
	if !ok {
		t.Skip("descriptor counting is unavailable on this platform")
	}

	root := t.TempDir()
	for d := range 20 {
		dir := filepath.Join(root, fmt.Sprintf("dir-%02d", d))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create dir: %v", err)
		}
		for f := range 20 {
			name := filepath.Join(dir, fmt.Sprintf("file-%02d.txt", f))
			if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
				t.Fatalf("create file: %v", err)
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	changes, err := Watch(ctx, root)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	watching, _ := openDescriptors(t)
	if watching <= baseline {
		t.Fatalf("watching descriptors = %d, want more than the %d baseline", watching, baseline)
	}

	cancel()
	for range changes { //nolint:revive // drain until the watcher closes the channel
	}

	// Teardown is asynchronous; poll rather than sleep a fixed interval.
	deadline := time.Now().Add(10 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		after, _ = openDescriptors(t)
		if after <= baseline {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("descriptors after cancel = %d, want at most the %d baseline (leaked %d of the %d opened)",
		after, baseline, after-baseline, watching-baseline)
}

// skipsNodeModules is a WatchSkipping filter for a tree of generated files.
func skipsNodeModules(rel string) bool {
	return slices.Contains(strings.Split(rel, "/"), "node_modules")
}

// writeGeneratedTree writes 2,000 files under root/node_modules, about what a
// small npm install writes.
func writeGeneratedTree(t *testing.T, root string) {
	t.Helper()
	for d := range 50 {
		dir := filepath.Join(root, "node_modules", fmt.Sprintf("pkg-%02d", d))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create dir: %v", err)
		}
		for f := range 40 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%02d.js", f)), []byte("x"), 0o600); err != nil {
				t.Fatalf("create file: %v", err)
			}
		}
	}
}

// assertFewDescriptors fails when the watch holds a descriptor for each
// generated file, as the kqueue backend does for a watched directory.
func assertFewDescriptors(t *testing.T, baseline int) {
	t.Helper()
	count, _ := openDescriptors(t)
	if grown := count - baseline; grown > 100 {
		t.Fatalf("open descriptors grew by %d for a skipped tree of 2000 files", grown)
	}
}

func TestWatchSkippingHoldsNoDescriptorsForASkippedTree(t *testing.T) {
	baseline, ok := openDescriptors(t)
	if !ok {
		t.Skip("descriptor counting is unavailable on this platform")
	}
	root := t.TempDir()
	writeGeneratedTree(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := WatchSkipping(ctx, root, skipsNodeModules); err != nil {
		t.Fatalf("watch: %v", err)
	}
	assertFewDescriptors(t, baseline)
}

// An install usually runs while the artifact is open, so the tree appears
// under a live watch.
func TestWatchSkippingLeavesOutATreeCreatedWhileWatching(t *testing.T) {
	baseline, ok := openDescriptors(t)
	if !ok {
		t.Skip("descriptor counting is unavailable on this platform")
	}
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes, err := WatchSkipping(ctx, root, skipsNodeModules)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	writeGeneratedTree(t, root)
	select {
	case <-changes:
		t.Fatal("a skipped tree reported a change")
	case <-time.After(300 * time.Millisecond):
	}
	assertFewDescriptors(t, baseline)

	if err := os.WriteFile(filepath.Join(root, "plan.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	select {
	case <-changes:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the change outside the skipped tree")
	}
}
