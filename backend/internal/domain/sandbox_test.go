package domain

import (
	"strings"
	"testing"
)

func TestSandboxConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     SandboxConfig
		wantErr string
	}{
		{name: "zero is off", cfg: SandboxConfig{}},
		{name: "off keeps image", cfg: SandboxConfig{Mode: SandboxModeOff, Image: "ubuntu:24.04"}},
		{name: "per-agent", cfg: SandboxConfig{Mode: SandboxModePerAgent, Image: "ghcr.io/acme/dev@sha256:abc", CPUs: "1.5", Memory: "4g", PidsLimit: 512}},
		{name: "shared", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "node:22"}},
		{name: "unknown mode", cfg: SandboxConfig{Mode: "vm", Image: "x"}, wantErr: `sandbox.mode: unknown mode "vm"`},
		{name: "enabled needs image", cfg: SandboxConfig{Mode: SandboxModeShared}, wantErr: "sandbox.image"},
		{name: "flag injection", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "--privileged"}, wantErr: "sandbox.image"},
		{name: "bad cpus", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "x", CPUs: "two"}, wantErr: "sandbox.cpus"},
		{name: "bad memory", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "x", Memory: "4 GB"}, wantErr: "sandbox.memory"},
		{name: "negative pids", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "x", PidsLimit: -1}, wantErr: "sandbox.pidsLimit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestEffectiveSandboxRoleOverrideReplacesWholeBlock(t *testing.T) {
	base := SandboxConfig{Mode: SandboxModeShared, Image: "node:22", Memory: "4g"}
	if got := EffectiveSandbox(base, RoleOverride{}); got != base {
		t.Fatalf("no override = %#v, want base", got)
	}
	off := SandboxConfig{Mode: SandboxModeOff}
	if got := EffectiveSandbox(base, RoleOverride{Sandbox: &off}); got != off {
		t.Fatalf("override = %#v, want %#v", got, off)
	}
}

func TestSessionSandboxAppliesRoleRules(t *testing.T) {
	cfg := ProjectConfig{Sandbox: SandboxConfig{Mode: SandboxModePerAgent, Image: "node:22"}}
	if SessionSandbox(KindOrchestrator, cfg).Enabled() {
		t.Fatal("orchestrator inherited the project sandbox")
	}
	if got := SessionSandbox(KindWorker, cfg); got != cfg.Sandbox {
		t.Fatalf("worker = %#v, want project block", got)
	}
	shared := SandboxConfig{Mode: SandboxModeShared, Image: "node:22"}
	cfg.Orchestrator.Sandbox = &shared
	off := SandboxConfig{Mode: SandboxModeOff}
	cfg.Worker.Sandbox = &off
	if got := SessionSandbox(KindOrchestrator, cfg); got != shared {
		t.Fatalf("orchestrator = %#v, want explicit block", got)
	}
	if SessionSandbox(KindWorker, cfg).Enabled() {
		t.Fatal("worker override to off was ignored")
	}
}

func TestProjectConfigValidateChecksSandbox(t *testing.T) {
	cfg := DefaultProjectConfig()
	cfg.Sandbox = SandboxConfig{Mode: SandboxModePerAgent}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.image") {
		t.Fatalf("project sandbox error = %v", err)
	}
	cfg = DefaultProjectConfig()
	cfg.Worker.Sandbox = &SandboxConfig{Mode: "bogus"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "worker.sandbox.mode") {
		t.Fatalf("worker sandbox error = %v", err)
	}
}
