package systeminstall

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func envValue(env []string, key string) string {
	value := ""
	for _, entry := range env {
		if name, v, ok := strings.Cut(entry, "="); ok && name == key {
			value = v // the last entry wins, as it does for the process
		}
	}
	return value
}

// Every recipe pins the release, installs inside the release directory, keeps
// the user's HOME and PATH out of the installer, and names where the CLI lands.
func TestOfficialManagedRecipesInstallInsideTheRelease(t *testing.T) {
	dir := "/data/harnesses/x/versions/v3"
	for target, want := range map[Target]struct {
		env  map[string]string
		args []string
	}{
		TargetOpencode: {env: map[string]string{"VERSION": "1.19.3"}, args: []string{"--no-modify-path"}},
		TargetKimi:     {env: map[string]string{"KIMI_INSTALL_DIR": dir, "KIMI_VERSION": "1.19.3", "KIMI_NO_MODIFY_PATH": "1"}},
		TargetGoose:    {env: map[string]string{"GOOSE_BIN_DIR": dir + "/bin", "GOOSE_VERSION": "v1.19.3", "CONFIGURE": "false"}},
		TargetMemcode:  {env: map[string]string{"MEMCODE_INSTALL_DIR": dir + "/bin", "MEMCODE_VERSION": "v1.19.3"}},
		TargetFX:       {env: map[string]string{"FX_INSTALL_DIR": dir + "/bin"}, args: []string{"v1.19.3"}},
		TargetOMP:      {env: map[string]string{"PI_INSTALL_DIR": dir + "/bin"}, args: []string{"--binary", "--ref", "v1.19.3"}},
		TargetQwen: {env: map[string]string{
			"QWEN_INSTALL_LIB_DIR": dir + "/lib", "QWEN_INSTALL_BIN_DIR": dir + "/bin",
			"QWEN_INSTALL_VERSION": "1.19.3", "QWEN_INSTALL_METHOD": "standalone", "QWEN_NO_MODIFY_PATH": "1",
		}},
		TargetCodex: {env: map[string]string{
			"CODEX_HOME": dir + "/codex-home", "CODEX_INSTALL_DIR": dir + "/bin",
			"CODEX_RELEASE": "1.19.3", "CODEX_NON_INTERACTIVE": "1",
		}},
		TargetOpencodeV2: {env: map[string]string{"VERSION": "1.19.3"}, args: []string{"--no-modify-path"}},
		TargetAmp:        {env: map[string]string{"AMP_HOME": dir, "AMP_VERSION": "1.19.3"}},
		TargetAutohand: {env: map[string]string{
			"AUTOHAND_INSTALL_DIR": dir + "/bin", "AUTOHAND_VERSION": "1.19.3",
			"AUTOHAND_SKIP_ZIT_INSTALL": "1", "AUTOHAND_SKIP_COMPUTER_CONTROL_INSTALL": "1", "AUTOHAND_INSTALL_FIRST_RUN": "no",
		}},
		TargetAgy:   {args: []string{"--dir", dir + "/bin"}},
		TargetDroid: {},
	} {
		t.Run(string(target), func(t *testing.T) {
			recipe, ok := officialManagedRecipes[target]
			if !ok {
				t.Fatal("no recipe")
			}
			env := recipe.env(dir, "1.19.3")
			if home := envValue(env, "HOME"); !strings.HasPrefix(home, dir+"/") {
				t.Fatalf("HOME = %q, want a directory inside the release", home)
			}
			if path := envValue(env, "PATH"); path != managedInstallerPath {
				t.Fatalf("PATH = %q, want system tools only", path)
			}
			for key, value := range want.env {
				if got := envValue(env, key); got != value {
					t.Fatalf("%s = %q, want %q", key, got, value)
				}
			}
			if got := recipe.args(dir, "1.19.3"); !slices.Equal(got, want.args) {
				t.Fatalf("args = %q, want %q", got, want.args)
			}
			if recipe.binRel == "" || filepath.IsAbs(recipe.binRel) {
				t.Fatalf("binRel = %q, want a path inside the release", recipe.binRel)
			}
		})
	}
}

// An OpenCode installed with its official installer updates while a session
// runs it: the installer runs into a release of AO's own, and the user's copy
// and the running session's files are left alone.
func TestOfficialInstallerUpdatesBesideARunningSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed launchers are symlinks on Unix")
	}
	root := t.TempDir()
	s := newTestService("darwin", "bash")
	s.managedRoot = root
	s.releases = &releaseStoreStub{}
	s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "oc-1", Harness: domain.HarnessOpenCode}}}
	userCopy := "/Users/me/.opencode/bin/opencode"
	launcher := filepath.Join(root, "bin", "opencode")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		if target, err := filepath.EvalSymlinks(launcher); err == nil {
			return VerifyResult{ResolvedPath: launcher, Output: strings.TrimSpace(readFile(target))}, nil
		}
		return VerifyResult{ResolvedPath: userCopy, Output: "1.19.2"}, nil
	})
	var mu sync.Mutex
	var scripts []ports.InstallScriptCommand
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
		mu.Lock()
		scripts = append(scripts, command)
		mu.Unlock()
		// Like opencode.ai/install: $HOME/.opencode/bin/opencode at $VERSION.
		bin := filepath.Join(envValue(command.Env, "HOME"), ".opencode", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			return ports.InstallScriptResult{}, err
		}
		return ports.InstallScriptResult{}, os.WriteFile(filepath.Join(bin, "opencode"), []byte(envValue(command.Env, "VERSION")+"\n"), 0o755)
	})
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if len(argv) == 2 && argv[1] == "--version" {
			_, err := io.WriteString(stdout, strings.TrimSpace(readFile(argv[0]))+"\n")
			return err
		}
		return nil
	})
	s.mu.Lock()
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetOpencode: {
		AgentID: string(TargetOpencode), Status: UpdateStatusBehindLatest, LatestVersion: "1.19.3",
		MaintenanceMethod: "official-installer", CheckedAt: time.Now(),
	}}
	s.mu.Unlock()

	if _, err := s.StartAgentOperation(context.Background(), TargetOpencode, "official-installer", AgentOperationUpdate, ""); err != nil {
		t.Fatalf("update with a session running = %v, want it to install beside the session", err)
	}
	s.workers.Wait()
	job, _ := s.Status(context.Background(), TargetOpencode)
	if job.Status != StatusSucceeded {
		t.Fatalf("job = %+v", job)
	}
	if len(scripts) != 1 {
		t.Fatalf("installer ran %d times", len(scripts))
	}
	release := filepath.Join(root, string(TargetOpencode), "versions", "v1")
	if home := envValue(scripts[0].Env, "HOME"); !strings.HasPrefix(home, release+string(filepath.Separator)) {
		t.Fatalf("installer HOME = %q, want inside %s", home, release)
	}
	if got := envValue(scripts[0].Env, "VERSION"); got != "1.19.3" {
		t.Fatalf("installer VERSION = %q, want the advisory's release", got)
	}
	if target, err := filepath.EvalSymlinks(launcher); err != nil || !strings.HasPrefix(target, release) {
		t.Fatalf("launcher resolves to %q (%v), want the new release", target, err)
	}
}

// Droid's own script kills every running droid, so AO downloads the pinned
// release and its checksum from where the script gets them.
func TestDroidDownloadsThePinnedReleaseWithoutItsScript(t *testing.T) {
	argv, err := droidDownloadCommand("/rel/v2", "0.244.0", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://downloads.factory.ai/factory-cli/releases/0.244.0/darwin/arm64/droid"
	if len(argv) != 7 || argv[0] != "/bin/sh" || argv[4] != want || argv[5] != want+".sha256" || argv[6] != "/rel/v2/bin/droid" {
		t.Fatalf("argv = %q", argv)
	}
	if strings.Contains(argv[2], "kill") {
		t.Fatal("the download must never stop running droids")
	}
	if argv, _ := droidDownloadCommand("/rel/v2", "0.244.0", "linux", "amd64"); !strings.Contains(argv[4], "/linux/x64-baseline/") {
		t.Fatalf("Intel download = %q, want the baseline build", argv[4])
	}
	if _, err := droidDownloadCommand("/rel/v2", "", "darwin", "arm64"); err == nil {
		t.Fatal("an unpinned download must fail instead of guessing a release")
	}
}
