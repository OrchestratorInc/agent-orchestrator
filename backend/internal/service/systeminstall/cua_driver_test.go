package systeminstall

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Cua Driver ships official per-user installers for all three platforms, so
// the plan is the vendor script everywhere: no package manager, no root.
func TestPlanForCuaDriver(t *testing.T) {
	tests := []struct {
		name            string
		goos            string
		found           []string
		wantURL         string
		wantInterpreter string
		wantUnsupported bool
	}{
		{name: "darwin runs install.sh with bash", goos: "darwin", found: []string{"bash"}, wantURL: cuaDriverUnixInstaller, wantInterpreter: "/usr/bin/bash"},
		{name: "linux runs install.sh with bash", goos: "linux", found: []string{"bash"}, wantURL: cuaDriverUnixInstaller, wantInterpreter: "/usr/bin/bash"},
		{name: "windows runs install.ps1 with PowerShell", goos: "windows", found: []string{"powershell.exe"}, wantURL: cuaDriverWindowsInstaller, wantInterpreter: "/usr/bin/powershell.exe"},
		{name: "without bash it is manual", goos: "linux", wantUnsupported: true},
		{name: "unknown platforms are manual", goos: "plan9", found: []string{"bash"}, wantUnsupported: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := newTestService(tt.goos, tt.found...).planFor(TargetCuaDriver)
			if plan.Target != TargetCuaDriver {
				t.Fatalf("Target = %q", plan.Target)
			}
			if plan.DocsURL != cuaDriverDocs {
				t.Fatalf("DocsURL = %q, want %q", plan.DocsURL, cuaDriverDocs)
			}
			if plan.Unsupported != tt.wantUnsupported {
				t.Fatalf("Unsupported = %v, want %v (reason=%q)", plan.Unsupported, tt.wantUnsupported, plan.Reason)
			}
			if tt.wantUnsupported {
				return
			}
			if plan.Script == nil || plan.Script.URL != tt.wantURL || plan.Script.Interpreter[0] != tt.wantInterpreter {
				t.Fatalf("Script = %+v, want %s run by %s", plan.Script, tt.wantURL, tt.wantInterpreter)
			}
		})
	}
}

func TestCuaDriverIsASystemTarget(t *testing.T) {
	if !Valid(TargetCuaDriver) || !IsSystemTarget(TargetCuaDriver) || IsAgentTarget(TargetCuaDriver) {
		t.Fatalf("cua-driver must be a valid system target and not a harness")
	}
}

// The installer links cua-driver into ~/.local/bin, which a desktop-launched
// daemon's PATH usually lacks. The registered resolver finds it there, so a
// successful install is not reported as "still not in PATH".
func TestStartCuaDriverRunsScriptAndConfirmsThroughResolver(t *testing.T) {
	s := newTestService("darwin", "bash")
	var ran ports.InstallScriptCommand
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, stdout, _ io.Writer) (ports.InstallScriptResult, error) {
		ran = command
		_, _ = io.WriteString(stdout, "installed cua-driver\n")
		return ports.InstallScriptResult{SHA256: "abc123"}, nil
	})
	s.SetSystemTargetResolver(TargetCuaDriver, func(context.Context) (string, error) {
		return "/Users/test/.local/bin/cua-driver", nil
	})

	job, err := s.Start(context.Background(), TargetCuaDriver)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !strings.Contains(job.Command, cuaDriverUnixInstaller) {
		t.Fatalf("Command = %q, want the installer URL", job.Command)
	}
	waitForStatus(t, s, TargetCuaDriver, StatusSucceeded)
	if ran.URL != cuaDriverUnixInstaller {
		t.Fatalf("ran %q, want %q", ran.URL, cuaDriverUnixInstaller)
	}
	if !containsEnv(ran.Env, "NONINTERACTIVE=1") {
		t.Fatalf("installer env = %v, want NONINTERACTIVE=1", ran.Env)
	}
	final, _ := s.Status(context.Background(), TargetCuaDriver)
	if !strings.Contains(final.Output, "sha256: abc123") {
		t.Fatalf("Output = %q, want the script hash", final.Output)
	}
}

func TestStartCuaDriverFailsWhenBinaryCannotBeFound(t *testing.T) {
	s := newTestService("linux", "bash")
	s.installScripts = installScriptRunnerFunc(func(context.Context, ports.InstallScriptCommand, io.Writer, io.Writer) (ports.InstallScriptResult, error) {
		return ports.InstallScriptResult{}, nil
	})
	s.SetSystemTargetResolver(TargetCuaDriver, func(context.Context) (string, error) {
		return "", errors.New("not found")
	})

	if _, err := s.Start(context.Background(), TargetCuaDriver); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, TargetCuaDriver, StatusFailed)
	final, _ := s.Status(context.Background(), TargetCuaDriver)
	if !strings.Contains(final.Error, "still not in PATH") {
		t.Fatalf("Error = %q", final.Error)
	}
}

func TestStartCuaDriverReportsInstallerFailure(t *testing.T) {
	s := newTestService("darwin", "bash")
	s.installScripts = installScriptRunnerFunc(func(context.Context, ports.InstallScriptCommand, io.Writer, io.Writer) (ports.InstallScriptResult, error) {
		return ports.InstallScriptResult{}, errors.New("exit status 1")
	})

	if _, err := s.Start(context.Background(), TargetCuaDriver); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, TargetCuaDriver, StatusFailed)
	final, _ := s.Status(context.Background(), TargetCuaDriver)
	if final.Error != "exit status 1" {
		t.Fatalf("Error = %q", final.Error)
	}
}

func containsEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}
