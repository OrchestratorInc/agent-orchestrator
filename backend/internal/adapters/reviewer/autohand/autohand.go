// Package autohand adapts Autohand as an experimental user-approved AO reviewer.
package autohand

import (
	"context"
	"strings"

	workerautohand "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/autohand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// HostTrustWarning documents that Autohand retains its normal approval flow.
const HostTrustWarning = "experimental user-approved reviewer: Autohand uses normal permission prompts; AO does not enable --yes or --unrestricted"

// Reviewer builds Autohand's reviewer command.
type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

// New returns the production Autohand reviewer adapter.
func New() *Reviewer { return &Reviewer{resolveBinary: workerautohand.ResolveAutohandBinary} }

// Harness returns Autohand's reviewer identity.
func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerAutohand }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)

// ReviewCommand launches Autohand with native system-prompt file support and
// no unattended approval flag, leaving tool requests visible to the user.
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	env := map[string]string{workerautohand.NoBrowserEnv: "1"}
	if strings.TrimSpace(inv.TaskPromptRoot) == "" {
		return ports.ReviewCommandSpec{Argv: []string{binary}, Env: env}, nil
	}
	argv := []string{binary, "--path", inv.WorkspacePath, "--sys-prompt", inv.SystemPromptFile, "--", inv.Prompt}
	return ports.ReviewCommandSpec{Argv: argv, Env: env}, nil
}

// ReviewRestoreCommand restores a recorded Autohand reviewer pane by
// relaunching the reviewer command with the current task context.
func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

// ReviewMessage returns the next AO-owned task reference.
func (*Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

// ReviewCancel interrupts Autohand's current operation while preserving the pane.
func (*Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 2}, nil
}
