package shellterm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectUnixShellsDeduplicatesSymlinksAndKeepsTheLinkPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell detection")
	}
	root := t.TempDir()
	cellar := filepath.Join(root, "Cellar", "fish", "4.1", "bin", "fish")
	writeTestExecutable(t, cellar)
	brew := filepath.Join(root, "brew", "bin")
	if err := os.MkdirAll(brew, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cellar, filepath.Join(brew, "fish")); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(root, "bin")
	writeTestExecutable(t, filepath.Join(system, "zsh"))
	writeTestExecutable(t, filepath.Join(system, "bash"))
	if err := os.WriteFile(filepath.Join(system, "nu"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", filepath.Join(system, "zsh"))

	listed := []string{filepath.Join(system, "zsh"), filepath.Join(brew, "fish"), "relative/sh", filepath.Join(root, "missing")}
	catalog := detectUnixShells(listed, []string{brew, system}, []string{"zsh", "bash", "fish", "nu"})

	want := []AvailableShell{
		{ID: filepath.Join(system, "bash"), Name: "bash", Path: filepath.Join(system, "bash")},
		{ID: filepath.Join(brew, "fish"), Name: "fish", Path: filepath.Join(brew, "fish")},
		{ID: filepath.Join(system, "zsh"), Name: "zsh", Path: filepath.Join(system, "zsh")},
	}
	if len(catalog.Shells) != len(want) {
		t.Fatalf("shells = %+v, want %+v", catalog.Shells, want)
	}
	for i := range want {
		if catalog.Shells[i] != want[i] {
			t.Fatalf("shells[%d] = %+v, want %+v", i, catalog.Shells[i], want[i])
		}
	}
	if catalog.Default.ID != "auto" || catalog.Default.Path != filepath.Join(system, "zsh") || catalog.Default.Name != "zsh" {
		t.Fatalf("default = %+v", catalog.Default)
	}
}

func TestCheckShellPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	shell := filepath.Join(home, ".local", "bin", "fish-ao")
	writeTestExecutable(t, shell)
	plain := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := CheckShellPath("~/.local/bin/fish-ao"); err != nil || got != shell {
		t.Fatalf("CheckShellPath(~ path) = %q, %v; want %q", got, err, shell)
	}
	for _, bad := range []string{"", plain, home, "relative/shell", filepath.Join(home, "missing")} {
		if got, err := CheckShellPath(bad); err == nil {
			t.Fatalf("CheckShellPath(%q) = %q, want an error", bad, got)
		}
	}
}

func TestResolveUnixShellHonorsSelectedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell selection")
	}
	dir := t.TempDir()
	fish := filepath.Join(dir, "fish")
	writeTestExecutable(t, fish)
	t.Setenv("SHELL", "/bin/sh")

	if argv, fallback := resolveUserLoginShell(fish); fallback || len(argv) != 1 || argv[0] != fish {
		t.Fatalf("selected path argv = %v fallback = %v, want [%s]", argv, fallback, fish)
	}
	for _, auto := range []string{"", "auto", " AUTO "} {
		if argv, fallback := resolveUserLoginShell(auto); fallback || len(argv) != 1 || argv[0] != "/bin/sh" {
			t.Fatalf("%q argv = %v fallback = %v, want $SHELL", auto, argv, fallback)
		}
	}
	if argv, fallback := resolveUserLoginShell(filepath.Join(dir, "missing")); !fallback || len(argv) != 1 || argv[0] != "/bin/sh" {
		t.Fatalf("missing path argv = %v fallback = %v, want $SHELL with fallback", argv, fallback)
	}
}

func TestResolveUnixShellIgnoresWindowsSelectors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell selection")
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PATH", t.TempDir())
	for _, selector := range []string{"git-bash", "powershell", "cmd", "wsl", "pwsh", "Git-Bash"} {
		if argv, fallback := resolveUserLoginShell(selector); fallback || len(argv) != 1 || argv[0] != "/bin/sh" {
			t.Fatalf("%q argv = %v fallback = %v, want $SHELL without an error", selector, argv, fallback)
		}
	}
}
