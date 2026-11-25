package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// A receive that fails to start the session reports why, not a bare 500.
func TestTransferErrorKeepsSpawnCodes(t *testing.T) {
	err := transferError(fmt.Errorf("receive: spawn: %w: tmux required on macOS", ports.ErrRuntimePrerequisite))
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "RUNTIME_PREREQUISITE_MISSING" {
		t.Fatalf("transferError(missing tmux) = %v", err)
	}
}

func TestTransferErrorNamesWhatBlocksTheMove(t *testing.T) {
	for _, test := range []struct {
		err        error
		code, text string
	}{
		{fmt.Errorf("transfer s1: push: %w: a merge", ports.ErrTransferGitBusy), "TRANSFER_GIT_BUSY", "in the middle of a merge"},
		{fmt.Errorf("transfer s1: push: %w: unresolved conflicts", ports.ErrTransferGitBusy), "TRANSFER_GIT_BUSY", "unresolved conflicts here"},
		{fmt.Errorf("transfer s1: push: %w: vendor/inner", ports.ErrTransferNestedRepo), "TRANSFER_NESTED_REPO", "vendor/inner"},
		{fmt.Errorf("transfer s1: %w: libs/core", ports.ErrTransferSubmodule), "TRANSFER_SUBMODULE", "The submodule libs/core has work"},
		{fmt.Errorf("transfer s1: push: %w: ! [rejected] (fetch first)", ports.ErrTransferPush), "TRANSFER_PUSH_REJECTED", "rewritten"},
		{fmt.Errorf("transfer s1: push: %w: pre-push hook declined", ports.ErrTransferPush), "TRANSFER_PUSH_FAILED", "pre-push hook declined"},
		{fmt.Errorf("receive: %w: Permission denied (publickey)", ports.ErrTransferFetch), "TRANSFER_FETCH_FAILED", "Permission denied"},
		{fmt.Errorf("transfer s1: push: %w: feat/x", ports.ErrDetachedHead), "TRANSFER_DETACHED_HEAD", "feat/x"},
		{fmt.Errorf("%w: feat/x", ports.ErrTransferBranchDiverged), "TRANSFER_BRANCH_DIVERGED", "feat/x on that computer"},
		{fmt.Errorf("spawn s1: %w: \"feat/x\" is checked out at \"/tmp/w\"", ports.ErrWorkspaceBranchCheckedOutElsewhere), "TRANSFER_BRANCH_IN_USE", "/tmp/w"},
		{fmt.Errorf("%w: %w: kill failed", sessionmanager.ErrTransferUnconfirmed, ports.ErrRuntimePrerequisite), "TRANSFER_UNCONFIRMED", "may have started"},
		{sessionmanager.ErrTransferNotPending, "TRANSFER_NOT_PENDING", "cancelled"},
		{errors.New("transfer s1: capture uncommitted work: open locked.txt: permission denied"), "TRANSFER_FAILED", "locked.txt"},
	} {
		var apiErr *apierr.Error
		if err := transferError(test.err); !errors.As(err, &apiErr) || apiErr.Code != test.code || !strings.Contains(apiErr.Message, test.text) {
			t.Errorf("transferError(%v) = %v", test.err, err)
		}
	}
}
