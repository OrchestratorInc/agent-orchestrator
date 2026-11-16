// Package amp adapts Amp's normal interactive TUI for AO reviews.
package amp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workeramp "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/amp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Reviewer builds Amp's interactive reviewer command.
type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

// New returns the production Amp reviewer adapter.
func New() *Reviewer { return &Reviewer{resolveBinary: workeramp.ResolveAmpBinary} }

// Harness returns Amp's reviewer identity.
func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerAmp }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerPromptReadinessProvider = (*Reviewer)(nil)

// ReviewPromptReadinessHints reuses Amp's worker-TUI prompt markers.
func (*Reviewer) ReviewPromptReadinessHints(ctx context.Context) (ports.PromptReadinessHints, error) {
	return workeramp.New().PromptReadinessHints(ctx, ports.LaunchConfig{})
}

// ReviewCommand launches Amp's permanent interactive TUI. AO never uses
// --execute; the review task is injected after the pane starts.
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	modeArgs := reviewerModeArgs(inv.Config)
	if inv.TaskPromptRoot == "" {
		return ports.ReviewCommandSpec{Argv: append([]string{binary}, modeArgs...)}, nil
	}
	settingsPath, err := writeReviewerSettings(inv.TaskPromptRoot)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	message := inv.Prompt
	if inv.SystemPromptFile != "" {
		message = fmt.Sprintf("First read and follow the reviewer role in `%s`. Then %s", filepath.ToSlash(inv.SystemPromptFile), inv.Prompt)
	}
	return ports.ReviewCommandSpec{
		Argv:           append([]string{binary, "--settings-file", settingsPath}, modeArgs...),
		InitialMessage: message,
	}, nil
}

// ReviewRestoreCommand restores a recorded Amp reviewer pane by relaunching the
// reviewer command with the current AO-owned task context.
func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

// reviewerModeArgs launches the reviewer in the configured Amp mode. Amp's
// model picker is its mode list, so a configured model value is a mode, as it
// is for Amp workers. Values Amp does not list are dropped.
func reviewerModeArgs(config domain.AgentConfig) []string {
	mode := strings.TrimSpace(config.Mode)
	if mode == "" {
		mode = strings.TrimSpace(config.Model)
	}
	if mode == "" || !modelcatalog.Accepts(string(domain.ReviewerAmp), mode) {
		return nil
	}
	return []string{"--mode", mode}
}

func writeReviewerSettings(root string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create amp reviewer settings directory: %w", err)
	}
	settings := map[string]any{
		"amp.dangerouslyAllowAll":          false,
		"amp.remoteThreadCreation.enabled": false,
		"amp.updates.mode":                 "disabled",
		"amp.mcpServers":                   map[string]any{},
		"amp.permissions": []map[string]any{
			{"tool": "Read", "action": "allow"},
			{"tool": "Grep", "action": "allow"},
			{"tool": "Glob", "action": "allow"},
			{"tool": "Bash", "action": "ask"},
			{"tool": "Edit", "action": "reject"},
			{"tool": "Write", "action": "reject"},
			{"tool": "*", "action": "reject"},
		},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("encode amp reviewer settings: %w", err)
	}
	path := filepath.Join(root, "amp-settings.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write amp reviewer settings: %w", err)
	}
	return path, nil
}

// ReviewMessage returns the next AO-owned task reference.
func (*Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

// ReviewCancel interrupts Amp's current operation while preserving the pane.
func (*Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 2}, nil
}
