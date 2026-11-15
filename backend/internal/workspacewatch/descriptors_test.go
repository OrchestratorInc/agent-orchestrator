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

func skipsNodeModules(rel string) bool {
	return slices.Contains(strings.Split(rel, "/"), "node_modules")
}

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
