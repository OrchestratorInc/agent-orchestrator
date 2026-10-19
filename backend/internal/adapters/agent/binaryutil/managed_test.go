package binaryutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveBinaryPrefersAOsManagedLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows launchers are .cmd files; covered by the name test")
	}
	dir := t.TempDir()
	SetManagedBinDir(dir)
	t.Cleanup(func() { SetManagedBinDir("") })

	spec := BinarySpec{Label: "managed-test", Names: []string{"ao-managed-test-cli"}}
	if _, err := ResolveBinary(context.Background(), spec); err == nil {
		t.Fatal("resolved a binary that does not exist")
	}
	launcher := filepath.Join(dir, "ao-managed-test-cli")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveBinary(context.Background(), spec)
	if err != nil || got != launcher {
		t.Fatalf("ResolveBinary = %q, %v; want the managed launcher %q", got, err, launcher)
	}

	SetManagedBinDir("")
	if _, ok := ManagedBinary("ao-managed-test-cli"); ok {
		t.Fatal("the managed lookup stayed on after it was turned off")
	}
}

func TestManagedCandidatesStayInsideTheLauncherDir(t *testing.T) {
	for _, name := range []string{"../escape", `..\escape`, "a/b", ""} {
		if got := managedCandidates("/managed", name); len(got) != 0 {
			t.Fatalf("managedCandidates(%q) = %v", name, got)
		}
	}
}
