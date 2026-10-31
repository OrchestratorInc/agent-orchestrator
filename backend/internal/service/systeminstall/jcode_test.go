package systeminstall

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

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

// install.ps1 configures a global hotkey or Alacritty only when asked, and
// reloads a running jcode server unless told not to; AO asks for neither.
func TestJCodeWindowsInstallerSkipsHotkeyAndServerReload(t *testing.T) {
	p := newTestService("windows", "powershell").planAgent(TargetJCode)
	if p.Script == nil || !slices.Contains(p.Script.Args, "-SkipHotkeySetup") || !slices.Contains(p.Script.Args, "-SkipAlacrittySetup") {
		t.Fatalf("args = %+v", p.Script)
	}
	if envValue(p.Script.Env, "JCODE_SKIP_SERVER_RELOAD") != "1" || envValue(p.Script.Env, "JCODE_NO_TELEMETRY") != "1" {
		t.Fatalf("env = %q", p.Script.Env)
	}
}

// The Unix installer always appends PATH lines to shell profiles, runs
// `setup-hotkey`, and reloads a running jcode server. AO's first install runs
// it into a release of its own with a private HOME, so none of that touches
// the user's files or their jcode server.
func TestJCodeFirstInstallRunsTheOfficialInstallerIntoAManagedRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed launchers are symlinks on Unix")
	}
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	root := t.TempDir()
	s := newTestService("linux", "bash", "curl")
	s.managedRoot = root
	s.releases = &releaseStoreStub{}
	var scripts []ports.InstallScriptCommand
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
		scripts = append(scripts, command)
		// Like jcode.sh/install: the launcher goes to $JCODE_INSTALL_DIR.
		bin := envValue(command.Env, "JCODE_INSTALL_DIR")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			return ports.InstallScriptResult{}, err
		}
		return ports.InstallScriptResult{}, os.WriteFile(filepath.Join(bin, "jcode"), []byte("jcode v0.99.1 (7206efd0c)\n"), 0o755)
	})
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if len(argv) == 2 && argv[1] == "--version" {
			_, err := io.WriteString(stdout, strings.TrimSpace(readFile(argv[0]))+"\n")
			return err
		}
		return nil
	})
	launcher := filepath.Join(root, "bin", "jcode")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		resolved, err := filepath.EvalSymlinks(launcher)
		if err != nil {
			return VerifyResult{}, err
		}
		return VerifyResult{ResolvedPath: launcher, Output: strings.TrimSpace(readFile(resolved))}, nil
	})

	if _, err := s.StartAgentOperation(context.Background(), TargetJCode, "official-installer", AgentOperationInstall, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	job, _ := s.Status(context.Background(), TargetJCode)
	if job.Status != StatusSucceeded || len(scripts) != 1 {
		t.Fatalf("job = %+v, installer runs = %d", job, len(scripts))
	}
	release := filepath.Join(root, string(TargetJCode), "versions", "v1")
	command := scripts[0]
	if !command.Isolated || !strings.HasPrefix(envValue(command.Env, "HOME"), release+string(filepath.Separator)) ||
		envValue(command.Env, "JCODE_SKIP_SERVER_RELOAD") != "1" || envValue(command.Env, "JCODE_NO_TELEMETRY") != "1" {
		t.Fatalf("installer command = %+v", command)
	}
	assertResolvesInto(t, launcher, release)
	if entries, _ := os.ReadDir(userHome); len(entries) != 0 {
		t.Fatalf("install wrote into the user's HOME: %v", entries)
	}
}
