package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedLauncherUsesExecutableOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix managed launchers use symlinks")
	}
	for _, tt := range []struct {
		name      string
		binary    string
		record    string
		method    string
		wantOwner bool
	}{
		{"external official install", "native/bin/codex", "", "official-installer", true},
		{"external install with stale managed record", "native/bin/codex", `{"method":"npm","dir":"v1"}`, "official-installer", true},
		{"external install cannot inherit stale owner", "native/bin/codex", `{"method":"npm","dir":"v1"}`, "npm", false},
		{"external Homebrew install", "homebrew/Caskroom/codex/0.163.0/codex", "", "official-installer", false},
		{"managed install needs a record", "harnesses/codex/versions/v1/bin/codex", "", "official-installer", false},
		{"managed npm install", "harnesses/codex/versions/v1/bin/codex", `{"method":"npm","dir":"v1"}`, "npm", true},
		{"managed npm install rejects official installer", "harnesses/codex/versions/v1/bin/codex", `{"method":"npm","dir":"v1"}`, "official-installer", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			s := newTestService("darwin", "sh", "npm")
			s.managedRoot = filepath.Join(root, "harnesses")
			binary := filepath.Join(root, filepath.FromSlash(tt.binary))
			writeFileAt(t, binary, "codex-cli 0.163.0")
			launcher := filepath.Join(s.ManagedBinDir(), "codex")
			if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(binary, launcher); err != nil {
				t.Fatal(err)
			}
			if tt.record != "" {
				writeFileAt(t, filepath.Join(s.managedTargetDir(TargetCodex), "managed.json"), tt.record)
			}
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: launcher, Output: "codex-cli 0.163.0"}, nil
			})
			baseline, err := s.confirmInstalledOwner(context.Background(), TargetCodex, Plan{Target: TargetCodex, Method: tt.method, Package: "@openai/codex"})
			if !tt.wantOwner {
				if !errors.Is(err, ErrInstallOwner) {
					t.Fatalf("ownership error = %v, want ErrInstallOwner", err)
				}
				return
			}
			if err != nil || baseline == nil || baseline.version != "0.163.0" || baseline.path != launcher {
				t.Fatalf("baseline = %+v, error = %v", baseline, err)
			}
		})
	}
}

func TestManagedUpdateAdoptsExternalLauncher(t *testing.T) {
	for _, method := range []string{"npm", "official-installer"} {
		t.Run(method, func(t *testing.T) {
			f := newManagedCodexFixture(t, nil)
			f.s.executables = executableFinderFunc(lookPathFound("npm", "sh"))
			f.s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
				bin := envValue(command.Env, "CODEX_INSTALL_DIR")
				if err := os.MkdirAll(bin, 0o755); err != nil {
					return ports.InstallScriptResult{}, err
				}
				return ports.InstallScriptResult{}, os.WriteFile(filepath.Join(bin, "codex"), []byte(envValue(command.Env, "CODEX_RELEASE")), 0o755)
			})
			external := filepath.Join(t.TempDir(), "bin", "codex")
			writeFileAt(t, external, "1.0.0")
			launcher := filepath.Join(f.root, "bin", "codex")
			if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, launcher); err != nil {
				t.Fatal(err)
			}
			for _, version := range []string{"1.1.0", "1.2.0"} {
				f.s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: {
					AgentID: string(TargetCodex), Status: UpdateStatusBehindLatest, LatestVersion: version,
					MaintenanceMethod: method, CheckedAt: time.Now(),
				}}
				if _, err := f.s.StartAgentOperation(context.Background(), TargetCodex, method, AgentOperationUpdate, version); err != nil {
					t.Fatalf("update to %s: %v", version, err)
				}
				f.s.workers.Wait()
				if job, err := f.s.Status(context.Background(), TargetCodex); err != nil || job.Status != StatusSucceeded {
					t.Fatalf("update to %s = %+v, error = %v", version, job, err)
				}
				if record, ok := f.s.readManagedRecord(TargetCodex); !ok || record.Method != method || record.Version != version {
					t.Fatalf("managed record = %+v, found = %t", record, ok)
				}
			}
			if got := readFile(external); got != "1.0.0" {
				t.Fatalf("external install was changed: %q", got)
			}
		})
	}
}
