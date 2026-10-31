package sessionmanager

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportFailureMessageShowsOnlyNotices(t *testing.T) {
	raw := fmt.Errorf("read source Git state: %w: %s", errors.New("exit status 128"), "fatal: not a git repository: /Users/me/secret-project/.git")
	if got := ImportFailureMessage(raw); got != importFailureFallback || strings.Contains(got, "/Users/me") {
		t.Fatalf("a raw Git failure reached the client: %q", got)
	}

	wrapped := fmt.Errorf("resume agent mer-1: %w", fmt.Errorf("copy original files: %w", importNotice("source files changed while copying; retry")))
	if got := ImportFailureMessage(wrapped); got != "source files changed while copying; retry" {
		t.Fatalf("a wrapped notice was not shown: %q", got)
	}

	joined := errors.Join(importNotice("session changed during workspace creation; retry"), errors.New("remove /Users/me/.ao/worktrees/mer-1: busy"))
	if got := ImportFailureMessage(joined); got != "session changed during workspace creation; retry" {
		t.Fatalf("a notice joined with a cleanup error was not shown alone: %q", got)
	}

	missing := fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, importNotice("the original provider history is missing; restore it and retry"))
	if !errors.Is(missing, ports.ErrChatResumeFailed) {
		t.Fatal("a notice must keep the resume-failed classification")
	}
	if got := ImportFailureMessage(missing); got != "the original provider history is missing; restore it and retry" {
		t.Fatalf("the missing-history notice was not shown: %q", got)
	}
}
