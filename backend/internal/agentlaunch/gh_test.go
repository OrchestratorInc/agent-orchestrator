package agentlaunch

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRealGHSkipsAllManagedWrappers(t *testing.T) {
	wrapper, older, realGH := t.TempDir(), t.TempDir(), t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, dir := range []string{wrapper, older, realGH} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("executable"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{wrapper, older} {
		if err := os.WriteFile(filepath.Join(dir, ghWrapperMarker), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := wrapper + string(os.PathListSeparator) + older + string(os.PathListSeparator) + realGH
	got, err := RealGH(path)
	if err != nil || got != filepath.Join(realGH, name) {
		t.Fatalf("realGH gh=%q err=%v", got, err)
	}
	if _, err := RealGH(wrapper); err == nil {
		t.Fatal("wrapper recursively resolved to itself")
	}
}

func TestGHDelegationPreservesAOWithoutReenteringWrapper(t *testing.T) {
	install, data, realGH := t.TempDir(), t.TempDir(), t.TempDir()
	aoName, ghName := "ao", "gh"
	if runtime.GOOS == "windows" {
		aoName += ".exe"
		ghName += ".exe"
	}
	exe := filepath.Join(install, aoName)
	if err := os.WriteFile(exe, []byte("AO"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realGH, ghName), []byte("gh"), 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := PinnedPATH(func() (string, error) { return exe, nil }, os.Getenv, map[string]string{"PATH": realGH}, data)
	if err != nil {
		t.Fatal(err)
	}
	delegated := GHPath(path)
	first := filepath.SplitList(delegated)[0]
	if _, err := os.Stat(filepath.Join(first, aoName)); err != nil {
		t.Fatalf("lost pinned ao: %v", err)
	}
	if _, err := os.Stat(filepath.Join(first, ghName)); !os.IsNotExist(err) {
		t.Fatalf("delegation reenters gh: %v", err)
	}
	got, err := RealGH(delegated)
	if err != nil || got != filepath.Join(realGH, ghName) {
		t.Fatalf("gh=%q err=%v", got, err)
	}
}
