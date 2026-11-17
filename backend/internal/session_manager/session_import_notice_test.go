package sessionmanager

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportFailureNoticeShowsOnlyNotices(t *testing.T) {
	raw := fmt.Errorf("read source Git state: %w: %s", errors.New("exit status 128"), "fatal: not a git repository: /Users/me/secret-project/.git")
	if got, ok := ImportFailureNotice(raw); ok || strings.Contains(got, "/Users/me") {
		t.Fatalf("a raw Git failure reached the client: %q", got)
	}

	wrapped := fmt.Errorf("resume agent mer-1: %w", fmt.Errorf("copy original files: %w", importNotice("source files changed while copying; retry")))
	if got, ok := ImportFailureNotice(wrapped); !ok || got != "source files changed while copying; retry" {
		t.Fatalf("a wrapped notice was not shown: %q", got)
	}

	joined := errors.Join(importNotice("session changed during workspace creation; retry"), errors.New("remove /Users/me/.ao/worktrees/mer-1: busy"))
	if got, ok := ImportFailureNotice(joined); !ok || got != "session changed during workspace creation; retry" {
		t.Fatalf("a notice joined with a cleanup error was not shown alone: %q", got)
	}

	missing := fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, importNotice("the original provider history is missing; restore it and retry"))
	if !errors.Is(missing, ports.ErrChatResumeFailed) {
		t.Fatal("a notice must keep the resume-failed classification")
	}
	if got, ok := ImportFailureNotice(missing); !ok || got != "the original provider history is missing; restore it and retry" {
		t.Fatalf("the missing-history notice was not shown: %q", got)
	}

	// A failed Setup step reads as it does for a native spawn; its output was
	// redacted when the step failed.
	setup := fmt.Errorf("prepare import: %w", &SetupStepError{Step: 2, Err: errors.New("exit status 1"), Output: "npm ERR! [redacted]"})
	if got, ok := ImportFailureNotice(setup); !ok || !strings.Contains(got, "step 2") || !strings.Contains(got, "npm ERR! [redacted]") {
		t.Fatalf("a failed Setup step was not shown: %q", got)
	}
}
