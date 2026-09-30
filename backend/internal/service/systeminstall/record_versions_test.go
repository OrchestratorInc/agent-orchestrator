package systeminstall

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRecordInstalledVersions(t *testing.T) {
	store := newInstallJobStoreFake()
	s := newTestService("linux")
	s.jobStore = store
	earlier := time.Now().UTC().Add(-time.Hour)
	s.jobs[TargetClaudeCode] = &Job{Target: TargetClaudeCode, Status: StatusSucceeded, Method: "npm", Version: "2.1.280", StartedAt: &earlier}
	s.jobs[TargetGoose] = &Job{Target: TargetGoose, Status: StatusInstalling, StartedAt: &earlier}
	s.verifier = harnessVerifierFunc(func(_ context.Context, target Target) (VerifyResult, error) {
		switch target {
		case TargetCodex:
			return VerifyResult{ResolvedPath: "/usr/bin/codex", Version: "0.154.0"}, nil
		case TargetClaudeCode:
			return VerifyResult{ResolvedPath: "/usr/bin/claude", Version: "2.1.283"}, nil
		case TargetGoose:
			t.Error("verified a harness with an active install job")
		}
		return VerifyResult{}, errors.New("not installed")
	})

	s.RecordInstalledVersions()

	codex, ok, _ := store.GetAgentInstallJob(context.Background(), string(TargetCodex))
	if !ok || codex.Version != "0.154.0" || codex.Status != string(StatusSucceeded) || codex.Method != "" {
		t.Fatalf("codex record = %+v, ok=%v; want succeeded, version 0.154.0, no method", codex, ok)
	}
	claude, ok, _ := store.GetAgentInstallJob(context.Background(), string(TargetClaudeCode))
	if !ok || claude.Version != "2.1.283" || claude.Method != "npm" {
		t.Fatalf("claude record = %+v, ok=%v; want version refreshed and method kept", claude, ok)
	}
	if _, ok, _ := store.GetAgentInstallJob(context.Background(), string(TargetAider)); ok {
		t.Fatal("recorded a job for a harness that is not installed")
	}
	if _, ok, _ := store.GetAgentInstallJob(context.Background(), string(TargetGoose)); ok {
		t.Fatal("overwrote a harness with an active install job")
	}
}
