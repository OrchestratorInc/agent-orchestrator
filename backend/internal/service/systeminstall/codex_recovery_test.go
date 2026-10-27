package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCodexUpdateRefusesInstallationChangedBeforeExecution(t *testing.T) {
	s := newTestService("darwin", "npm")
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	probes, mutations := 0, 0
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		probes++
		path := "/original/bin/codex"
		if probes > 1 {
			path = "/replacement/bin/codex"
		}
		return VerifyResult{ResolvedPath: path, Output: "codex 1.2.3"}, nil
	})
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
		mutations++
		return nil
	})
	if _, err := s.StartAgentOperation(t.Context(), TargetCodex, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	job, err := s.Status(t.Context(), TargetCodex)
	if err != nil || job.Status != StatusFailed || !strings.Contains(strings.ToLower(job.Error), "installation changed") || mutations != 0 {
		t.Fatalf("job=%+v err=%v mutations=%d; changed installation must not be updated", job, err, mutations)
	}
}

func TestCodexUnknownInstallationIsManualOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom", "codex")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("custom wrapper"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newTestService("darwin", "sh")
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: path, Output: "codex 1.2.3"}, nil
	})
	s.officialVersion = func(context.Context, Target) (string, error) { return "1.3.0", nil }
	advisory, err := s.UpdateAdvisory(t.Context(), TargetCodex, true)
	if err != nil || advisory.MaintenanceMethod != "" {
		t.Fatalf("advisory=%+v err=%v; a custom executable must remain manual-only", advisory, err)
	}
	if _, err := s.StartAgentOperation(t.Context(), TargetCodex, "official-installer", AgentOperationUpdate, ""); !errors.Is(err, ErrInstallOwner) {
		t.Fatalf("update error=%v; unverified ownership must be refused", err)
	}
}

func TestCodexFailedUpdaterInvalidatesEffectiveProvider(t *testing.T) {
	s := newTestService("darwin", "npm")
	invalidated := 0
	s.SetBinaryInvalidator(func(target Target) {
		if target != TargetCodex {
			t.Errorf("invalidated %s, want codex", target)
		}
		invalidated++
	})
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
		return errors.New("updater failed after replacing the executable")
	})
	job := &Job{Target: TargetCodex, Method: "npm", Status: StatusInstalling}
	s.runAgentOperation(t.Context(), Plan{Target: TargetCodex, Method: "npm", Command: []string{"npm", "install"}}, AgentOperationUpdate, job, nil)
	if job.Status != StatusFailed || invalidated == 0 {
		t.Fatalf("job=%+v invalidations=%d; failed replacement must refresh provider caches", job, invalidated)
	}
}

func TestCodexUpdateRejectsApprovalForAnotherInstallation(t *testing.T) {
	s := newTestService("darwin", "npm")
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	path := "/original/bin/codex"
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: path, Output: "codex 1.2.3"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	s.managedVersion = fixedManagedVersion("1.3.0", nil)
	advisory, err := s.UpdateAdvisory(t.Context(), TargetCodex, true)
	if err != nil || advisory.InstallationID == "" {
		t.Fatalf("advisory=%+v err=%v", advisory, err)
	}
	path = "/another-prefix/bin/codex"
	if _, err := s.StartAgentUpdate(t.Context(), TargetCodex, "npm", "1.3.0", advisory.InstallationID); !errors.Is(err, ErrInstallationChanged) {
		t.Fatalf("error=%v, want changed-installation refusal", err)
	}
}

func TestCodexNativeUpdateRequiresCapabilityAndUsesInstallationHome(t *testing.T) {
	for _, supportsUpdate := range []bool{false, true} {
		t.Run(fmt.Sprint(supportsUpdate), func(t *testing.T) {
			home := t.TempDir()
			binary := filepath.Join(home, "packages", "standalone", "release", "codex")
			if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("native release"), 0o755); err != nil {
				t.Fatal(err)
			}
			s := newTestService("darwin", "sh")
			t.Cleanup(func() { _ = s.Close(context.Background()) })
			s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
				if len(argv) != 2 || argv[0] != binary || argv[1] != "--help" {
					t.Fatalf("unexpected probe %v", argv)
				}
				if supportsUpdate {
					_, _ = io.WriteString(stdout, "Commands:\n  update  Update the standalone installation\n")
				}
				return nil
			})
			version := "1.2.3"
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: binary, Output: "codex " + version}, nil
			})
			s.officialVersion = func(context.Context, Target) (string, error) { return "1.3.0", nil }
			var mutations []ports.InstallCommand
			s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
				mutations = append(mutations, command)
				version = "1.3.0"
				return nil
			})
			advisory, err := s.UpdateAdvisory(t.Context(), TargetCodex, true)
			if err != nil {
				t.Fatal(err)
			}
			if !supportsUpdate {
				if advisory.MaintenanceMethod != "" {
					t.Fatalf("old CLI offered automatic maintenance: %+v", advisory)
				}
				return
			}
			if _, err := s.StartAgentUpdate(t.Context(), TargetCodex, "official-installer", "1.3.0", advisory.InstallationID); err != nil {
				t.Fatal(err)
			}
			s.workers.Wait()
			job, _ := s.Status(t.Context(), TargetCodex)
			if job.Status != StatusSucceeded || len(mutations) != 1 || strings.Join(mutations[0].Argv, " ") != binary+" update" || !slices.Contains(mutations[0].Env, "CODEX_HOME="+home) {
				t.Fatalf("job=%+v commands=%+v", job, mutations)
			}
		})
	}
}

func TestInstallerScopeWaitIsCancellableAndDifferentPrefixesRemainIndependent(t *testing.T) {
	s := newTestService("darwin", "npm")
	now := time.Now()
	first := Plan{Target: TargetCodex, Method: "npm", PackagePrefix: "/shared/npm"}
	release, err := s.lockInstaller(t.Context(), first, &Job{Target: TargetCodex, StartedAt: &now})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if unlock, err := s.lockInstaller(ctx, Plan{Target: TargetClaudeCode, Method: "npm", PackagePrefix: "/shared/npm"}, &Job{Target: TargetClaudeCode, StartedAt: &now}); err == nil {
		unlock()
		t.Fatal("a second mutable update entered the same npm prefix")
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	other, err := s.lockInstaller(ctx, Plan{Target: TargetClaudeCode, Method: "npm", PackagePrefix: "/independent/npm"}, &Job{Target: TargetClaudeCode, StartedAt: &now})
	if err != nil {
		t.Fatalf("independent prefix blocked: %v", err)
	}
	other()
}
