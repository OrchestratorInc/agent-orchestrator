package chat_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// A reviewer cannot fall back to a looser posture, so its preflight has to
// prove the read-only sandbox can start before AO spends a turn on it.
func TestPreflightReviewChatProbesTheReadOnlySandbox(t *testing.T) {
	var got ports.ChatSandboxProbeConfig
	svc := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: fakeDriver{
		probeSandbox: func(cfg ports.ChatSandboxProbeConfig) error {
			got = cfg
			return nil
		},
	}}})
	workspace := t.TempDir()

	if err := svc.PreflightReviewChat(context.Background(), domain.HarnessCodex, workspace); err != nil {
		t.Fatalf("PreflightReviewChat: %v", err)
	}
	if got.WorkspacePath != workspace {
		t.Fatalf("probe workspace = %q, want %q", got.WorkspacePath, workspace)
	}
}

func TestPreflightReviewChatSurfacesTheSandboxError(t *testing.T) {
	want := fmt.Errorf("%w: bwrap: setting up uid map: Permission denied", ports.ErrChatSandboxUnavailable)
	svc := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: fakeDriver{
		probeSandbox: func(ports.ChatSandboxProbeConfig) error { return want },
	}}})

	err := svc.PreflightReviewChat(context.Background(), domain.HarnessCodex, t.TempDir())
	if !errors.Is(err, ports.ErrChatSandboxUnavailable) {
		t.Fatalf("err = %v, want ErrChatSandboxUnavailable", err)
	}
	if !strings.Contains(err.Error(), "bwrap: setting up uid map: Permission denied") {
		t.Fatalf("err = %v, want the provider's message preserved", err)
	}
}

// Admission failures are cheaper and more specific than the probe, so the
// probe must not run when the harness cannot start at all.
func TestPreflightReviewChatDoesNotProbeWhenAdmissionFails(t *testing.T) {
	probed := false
	svc := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: fakeDriver{
		probe: func() error { return errors.New("codex is not installed") },
		probeSandbox: func(ports.ChatSandboxProbeConfig) error {
			probed = true
			return nil
		},
	}}})

	if err := svc.PreflightReviewChat(context.Background(), domain.HarnessCodex, t.TempDir()); err == nil {
		t.Fatal("PreflightReviewChat must surface the admission failure")
	}
	if probed {
		t.Fatal("sandbox probe ran after admission had already failed")
	}
}

// Drivers that do not implement the probe keep their previous behaviour.
func TestPreflightReviewChatAllowsDriversWithoutASandboxProbe(t *testing.T) {
	svc := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: &sequenceDriver{}}})

	if err := svc.PreflightReviewChat(context.Background(), domain.HarnessCodex, t.TempDir()); err != nil {
		t.Fatalf("PreflightReviewChat: %v", err)
	}
}
