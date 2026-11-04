package agentlaunch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedWindowsInstallSelectsOnlyCanonicalAO(t *testing.T) {
	if os.Getenv("AO_TEST_PIN_CHILD") == "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("IDENTITY=" + exe)
		return
	}
	dataDir := t.TempDir()
	shared, agent := t.TempDir(), t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(shared, "ao.exe"), filepath.Join(shared, "node.exe"), filepath.Join(agent, "ao.exe"), filepath.Join(agent, "node.exe")} {
		if err := os.WriteFile(path, binary, 0o700); err != nil {
			t.Fatal(err)
		} //nolint:gosec // executable test fixture
	}
	canonical := filepath.Join(shared, "ao.exe")
	executable := func() (string, error) { return canonical, nil }
	path, err := PinnedPATH(executable, os.Getenv, map[string]string{"PATH": agent + ";" + shared + ";" + os.Getenv("PATH")}, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": path}
	AugmentRuntimePATHForLaunchBinary(context.Background(), env, []string{filepath.Join(agent, "agent.exe")}, exec.LookPath, PinnedDir(executable, dataDir))
	cmd := exec.CommandContext(context.Background(), os.Getenv("ComSpec"), "/d", "/c", "call ao -test.run=^TestSharedWindowsInstallSelectsOnlyCanonicalAO$ & node -test.run=^TestSharedWindowsInstallSelectsOnlyCanonicalAO$")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "PATH") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+env["PATH"], "AO_TEST_PIN_CHILD=1")
	cmd.Dir = t.TempDir()
	output, err := cmd.CombinedOutput()
	if err != nil || !containsCanonicalAO(t, string(output), canonical) || !strings.Contains(string(output), "IDENTITY="+filepath.Join(agent, "node.exe")) {
		t.Fatalf("executable selection: %v\n%s", err, output)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal("Windows regression requires Git Bash: ", err)
	}
	bashCmd := exec.CommandContext(context.Background(), bash, "--noprofile", "--norc", "-c", "ao -test.run=^TestSharedWindowsInstallSelectsOnlyCanonicalAO$")
	bashCmd.Env = cmd.Env
	bashCmd.Dir = t.TempDir()
	bashOutput, err := bashCmd.CombinedOutput()
	if err != nil || !containsCanonicalAO(t, string(bashOutput), canonical) {
		t.Fatalf("Git Bash AO selection: %v\n%s", err, bashOutput)
	}
}

// TestWindowsPerUserInstallPinsShimDirectory covers issue #7171. A per-user
// install directory holds only ao.exe but is unreadable to sandboxes that run
// agent commands as another local identity, so pointing PATH at it leaves the
// agent unable to run `ao`. The pin has to land on the data-directory shim,
// whose ACLs come from the profile instead.
func TestWindowsPerUserInstallPinsShimDirectory(t *testing.T) {
	if os.Getenv("AO_TEST_PIN_CHILD") == "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("IDENTITY=" + exe)
		return
	}
	dataDir := t.TempDir()
	installDir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(installDir, "ao.exe")
	if err := os.WriteFile(canonical, binary, 0o700); err != nil { //nolint:gosec // executable test fixture
		t.Fatal(err)
	}
	executable := func() (string, error) { return canonical, nil }
	pinned := PinnedDir(executable, dataDir)
	if pinned == "" {
		t.Fatal("PinnedDir returned an empty directory")
	}
	if samePath(pinned, installDir) {
		t.Fatalf("PinnedDir = %q, want a shim outside the install directory", pinned)
	}
	if !strings.HasPrefix(strings.ToLower(pinned), strings.ToLower(filepath.Join(dataDir, "runtime", "ao-path"))) {
		t.Fatalf("PinnedDir = %q, want a directory below <data>/runtime/ao-path", pinned)
	}

	// A single-executable install directory still has to yield a runnable ao
	// through the shim alone, without reaching back into the install path.
	cmd := exec.CommandContext(context.Background(), filepath.Join(pinned, "ao.exe"), "-test.run=^TestWindowsPerUserInstallPinsShimDirectory$")
	cmd.Env = append(os.Environ(), "AO_TEST_PIN_CHILD=1")
	cmd.Dir = t.TempDir()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shimmed AO execution: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "IDENTITY="+filepath.Join(pinned, "ao.exe")) {
		t.Fatalf("shimmed AO identity: %s", output)
	}
	batch, err := os.ReadFile(filepath.Join(pinned, "ao.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(batch)), strings.ToLower(installDir)) {
		t.Fatalf("ao.cmd reaches into the install directory: %s", batch)
	}
}

// TestWindowsAOPathShimCopiesExecutable pins the copy semantics: a hard link
// would inherit the install file's security descriptor and stay unreadable to
// sandboxes, so the shim must be a real copy.
func TestWindowsAOPathShimCopiesExecutable(t *testing.T) {
	if os.Getenv("AO_TEST_PIN_CHILD") == "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("IDENTITY=" + exe)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(t.TempDir(), "ao.exe")
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, binary, 0o700); err != nil { //nolint:gosec // executable test fixture
		t.Fatal(err)
	}
	canonicalInfo, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	if err := ensureWindowsAOExecutable(shimDir, canonical); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(shimDir, "ao.exe")
	shimInfo, err := os.Stat(shim)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(canonicalInfo, shimInfo) {
		t.Fatal("shim AO exe is a hard link to the install executable; expected a copy so the shim directory's ACLs apply")
	}
	shimBinary, err := os.ReadFile(shim)
	if err != nil || !bytes.Equal(shimBinary, binary) {
		t.Fatalf("copied AO executable: %v, identical=%v", err, bytes.Equal(shimBinary, binary))
	}
	if !shimInfo.ModTime().Equal(canonicalInfo.ModTime()) {
		t.Fatalf("shim modtime = %v, want %v so repeat launches reuse the copy", shimInfo.ModTime(), canonicalInfo.ModTime())
	}
	if err := ensureWindowsAOExecutable(shimDir, canonical); err != nil {
		t.Fatalf("repeat pin: %v", err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal("Windows regression requires Git Bash: ", err)
	}
	cmd := exec.CommandContext(context.Background(), bash, "--noprofile", "--norc", "-c", "ao -test.run=^TestWindowsAOPathShimCopiesExecutable$")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "PATH") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+shimDir+";"+os.Getenv("PATH"), "AO_TEST_PIN_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(strings.ToLower(string(output)), strings.ToLower("IDENTITY="+shim)) {
		t.Fatalf("Git Bash copied AO selection: %v\n%s", err, output)
	}
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// containsCanonicalAO reports whether the child resolved `ao` to a binary that
// is byte-identical to canonical. The pin installs a copy, so file identity is
// program identity rather than a shared file handle.
func containsCanonicalAO(t *testing.T, output, canonical string) bool {
	t.Helper()
	want, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(want)
	for _, line := range strings.Split(output, "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "IDENTITY=")
		if !ok {
			continue
		}
		got, gotErr := os.ReadFile(path)
		if gotErr != nil {
			return false
		}
		return sha256.Sum256(got) == wantSum
	}
	return false
}
