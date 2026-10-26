package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type captureAgent struct {
	got        ports.LaunchConfig
	gotRestore ports.RestoreConfig
}

func (a *captureAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}
func (a *captureAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.got = cfg
	return []string{"agent", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`, "--", cfg.Prompt}, nil
}
func (a *captureAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}
func (a *captureAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error { return nil }
func (a *captureAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	a.gotRestore = cfg
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if id == "" {
		return nil, false, nil
	}
	return []string{"agent", "resume", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`, id}, true, nil
}
func (a *captureAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

func TestReviewCommandUsesReadOnlySandbox(t *testing.T) {
	t.Setenv("AO_PORT", "3103")
	t.Setenv("AO_DATA_DIR", "/wrong/data")
	t.Setenv("AO_RUN_FILE", "/wrong/running.json")
	agent := &captureAgent{}
	r := &Reviewer{agent: agent, executable: fixedAO}

	got, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID:      "review-w1",
		ReviewSessionID: "rv-1",
		WorkerSessionID: "w1",
		WorkspacePath:   "/ws/w1",
		DataDir:         "/tmp/ao data",
		RunFilePath:     "/tmp/ao data/running.json",
		Prompt:          "review it",
		SystemPrompt:    "review only",
	})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}

	want := []string{
		"agent",
		"--ask-for-approval", "never",
		"-c", `default_permissions="ao_reviewer"`,
		"-c", `permissions.ao_reviewer.extends=":read-only"`,
		"-c", "permissions.ao_reviewer.network.enabled=true",
		"-c", `shell_environment_policy.set.AO_PORT="3103"`,
		"-c", `shell_environment_policy.set.AO_DATA_DIR="/tmp/ao data"`,
		"-c", `shell_environment_policy.set.AO_RUN_FILE="/tmp/ao data/running.json"`,
		"-c", `shell_environment_policy.set.AO_REVIEW_SESSION_ID="rv-1"`,
		"-c", `shell_environment_policy.set.AO_REVIEW_WORKER_SESSION_ID="w1"`,
		"-c", `hooks.PreToolUse=[{hooks=[{type="command",command="'/opt/ao/bin/ao' hooks codex review-guard",timeout=30}]}]`,
		"--", "review it",
	}
	if !slices.Equal(got.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", got.Argv, want)
	}
	if agent.got.Permissions != ports.PermissionModeAuto {
		t.Fatalf("permissions = %q, want auto", agent.got.Permissions)
	}
	if agent.got.SystemPrompt != "review only" {
		t.Fatalf("system prompt = %q", agent.got.SystemPrompt)
	}
}

func TestReviewCommandEmitsConfiguredCodexEffort(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	got, err := New().ReviewCommand(context.Background(), ports.ReviewInvocation{
		Config: ports.AgentConfig{Effort: "high"},
		Prompt: "review it",
	})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if !slices.Contains(got.Argv, "model_reasoning_effort='high'") {
		t.Fatalf("review command %#v missing configured Codex effort", got.Argv)
	}
}

func TestReviewMessageReturnsTaskPrompt(t *testing.T) {
	got, err := (&Reviewer{}).ReviewMessage(context.Background(), ports.ReviewInvocation{Prompt: "next review"})
	if err != nil {
		t.Fatalf("ReviewMessage: %v", err)
	}
	if got != "next review" {
		t.Fatalf("message = %q", got)
	}
}

func TestReviewCancelSendsSingleEscapeInput(t *testing.T) {
	spec, err := (&Reviewer{}).ReviewCancel(context.Background())
	if err != nil {
		t.Fatalf("ReviewCancel: %v", err)
	}
	if spec.Mode != ports.ReviewCancelInput {
		t.Fatalf("cancel mode = %q, want %q", spec.Mode, ports.ReviewCancelInput)
	}
	if spec.Input != "\x1b" || len(spec.Inputs) != 0 {
		t.Fatalf("input = %q inputs = %#v, want single escape input", spec.Input, spec.Inputs)
	}
}

func TestReviewCommandUsesHiddenSystemPromptFile(t *testing.T) {
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}

	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		Prompt:           "Start the AO review task.",
		SystemPromptFile: "/ao/prompts/reviewer/system.md",
	}); err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if agent.got.Prompt != "Start the AO review task." || agent.got.SystemPrompt != "" || agent.got.SystemPromptFile != "/ao/prompts/reviewer/system.md" {
		t.Fatalf("launch config = %+v", agent.got)
	}
}

func TestReviewRestoreCommandUsesNativeSessionIDAndReadOnlySandbox(t *testing.T) {
	t.Setenv("AO_PORT", "3103")
	t.Setenv("AO_DATA_DIR", "/wrong/data")
	t.Setenv("AO_RUN_FILE", "/wrong/running.json")
	agent := &captureAgent{}
	r := &Reviewer{agent: agent, executable: fixedAO}

	got, ok, err := r.ReviewRestoreCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID:       "review-w1",
		ReviewSessionID:  "rv-1",
		WorkerSessionID:  "w1",
		AgentSessionID:   "codex-native-1",
		WorkspacePath:    "/ws/w1",
		DataDir:          "/tmp/ao data",
		RunFilePath:      "/tmp/ao data/running.json",
		SystemPromptFile: "/ao/prompts/reviewer/system.md",
	})
	if err != nil {
		t.Fatalf("ReviewRestoreCommand: %v", err)
	}
	if !ok {
		t.Fatal("ReviewRestoreCommand ok = false, want true")
	}
	if !got.NativeResumed {
		t.Fatal("ReviewRestoreCommand did not report native resume")
	}
	want := []string{
		"agent", "resume",
		"--ask-for-approval", "never",
		"-c", `default_permissions="ao_reviewer"`,
		"-c", `permissions.ao_reviewer.extends=":read-only"`,
		"-c", "permissions.ao_reviewer.network.enabled=true",
		"-c", `shell_environment_policy.set.AO_PORT="3103"`,
		"-c", `shell_environment_policy.set.AO_DATA_DIR="/tmp/ao data"`,
		"-c", `shell_environment_policy.set.AO_RUN_FILE="/tmp/ao data/running.json"`,
		"-c", `shell_environment_policy.set.AO_REVIEW_SESSION_ID="rv-1"`,
		"-c", `shell_environment_policy.set.AO_REVIEW_WORKER_SESSION_ID="w1"`,
		"-c", `hooks.PreToolUse=[{hooks=[{type="command",command="'/opt/ao/bin/ao' hooks codex review-guard",timeout=30}]}]`,
		"codex-native-1",
	}
	if !slices.Equal(got.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", got.Argv, want)
	}
	if agent.gotRestore.Session.ID != "review-w1" || agent.gotRestore.Session.WorkspacePath != "/ws/w1" {
		t.Fatalf("restore session = %+v", agent.gotRestore.Session)
	}
	if agent.gotRestore.Session.Metadata[ports.MetadataKeyAgentSessionID] != "codex-native-1" {
		t.Fatalf("restore metadata = %#v", agent.gotRestore.Session.Metadata)
	}
	if agent.gotRestore.Permissions != ports.PermissionModeAuto {
		t.Fatalf("restore permissions = %q, want auto", agent.gotRestore.Permissions)
	}
}

func TestNeverApproveRemovesEveryApprovalPath(t *testing.T) {
	got, err := neverApprove([]string{"codex", "-c", "x=1", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`, "--", "keep --ask-for-approval on-request"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "-c", "x=1", "--ask-for-approval", "never", "--", "keep --ask-for-approval on-request"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
	if got, _ := neverApprove([]string{"codex", "resume", "id-1"}); !slices.Equal(got, []string{"codex", "resume", "--ask-for-approval", "never", "id-1"}) {
		t.Fatalf("fallback argv = %#v", got)
	}
	for _, bypass := range []string{"--dangerously-bypass-approvals-and-sandbox", "--yolo"} {
		if _, err := neverApprove([]string{"codex", bypass, "--", "p"}); err == nil {
			t.Fatalf("neverApprove accepted %s", bypass)
		}
	}
}

func TestReviewPreflightRequiresTheSandboxToBlockWrites(t *testing.T) {
	resolve := func(context.Context) (string, error) { return "/bin/codex", nil }
	cases := []struct {
		name    string
		run     func(ctx context.Context, binary string, args ...string) ([]byte, error)
		wantErr bool
	}{
		{
			name: "profile enforced",
			run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if !slices.Contains(args, `default_permissions="ao_reviewer"`) || args[0] != "sandbox" {
					t.Fatalf("probe args = %#v", args)
				}
				if script := args[len(args)-1]; strings.Contains(script, ">") {
					return nil, errors.New("Operation not permitted")
				}
				return []byte("ao-reviewer-probe\n"), nil
			},
		},
		{
			name: "write gets through",
			run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				script := args[len(args)-1]
				if i := strings.Index(script, "> "); i >= 0 {
					path := strings.Trim(script[i+2:], `'"`)
					if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
						t.Fatal(err)
					}
					return nil, nil
				}
				return []byte("ao-reviewer-probe\n"), nil
			},
			wantErr: true,
		},
		{
			name: "profile cannot run",
			run: func(context.Context, string, ...string) ([]byte, error) {
				return nil, errors.New("unknown config key default_permissions")
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reviewer{agent: &captureAgent{}, resolveBinary: resolve, runSandbox: tc.run}
			if err := r.ReviewPreflight(context.Background(), "/ws"); (err != nil) != tc.wantErr {
				t.Fatalf("ReviewPreflight err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func fixedAO() (string, error) { return "/opt/ao/bin/ao", nil }

// TestReviewCommandRefusesLaunchWithoutGuard pins that a Codex reviewer never
// starts without its review-guard hook: Codex runs a command whose hook fails.
func TestReviewCommandRefusesLaunchWithoutGuard(t *testing.T) {
	r := &Reviewer{agent: &captureAgent{}, executable: func() (string, error) { return "", os.ErrNotExist }}
	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{ReviewerID: "review-w1", WorkspacePath: "/ws"}); err == nil {
		t.Fatal("ReviewCommand launched without a resolvable review guard; want an error")
	}
}
