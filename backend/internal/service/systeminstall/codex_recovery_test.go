package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
