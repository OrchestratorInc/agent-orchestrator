package droid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	workerdroid "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const settingsFilename = "droid-reviewer-settings.json"

const HostTrustWarning = "experimental host-trusted reviewer: Droid has no OS isolation; terminal users can invoke Bash mode, change interaction/autonomy settings, or enable additional integrations"

type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

func New() *Reviewer {
	return &Reviewer{resolveBinary: workerdroid.ResolveDroidBinary}
}

func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerDroid }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerPromptReadinessProvider = (*Reviewer)(nil)
var _ ports.ReviewerReusePolicy = (*Reviewer)(nil)

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if !filepath.IsAbs(binary) {
		return ports.ReviewCommandSpec{}, errors.New("droid reviewer: resolved binary must be absolute")
	}
	if strings.TrimSpace(inv.TaskPromptRoot) == "" {
		return ports.ReviewCommandSpec{Argv: []string{binary}}, nil
	}
	settingsPath, err := writeReviewerSettings(inv.TaskPromptRoot)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	argv := []string{binary, "--settings", settingsPath}
	if strings.TrimSpace(inv.SystemPromptFile) != "" {
		argv = append(argv, "--append-system-prompt-file", inv.SystemPromptFile)
	}
	if strings.TrimSpace(inv.WorkspacePath) != "" {
		argv = append(argv, "--cwd", inv.WorkspacePath)
	}
	if strings.TrimSpace(inv.Prompt) != "" {
		argv = append(argv, inv.Prompt)
	}
	return ports.ReviewCommandSpec{
		Argv: argv,
	}, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

func (*Reviewer) ReviewProcessReusable() bool { return false }

func (*Reviewer) ReviewPromptReadinessHints(ctx context.Context) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{InitialDelay: 2 * time.Second}, nil
}

func writeReviewerSettings(promptRoot string) (string, error) {
	if !filepath.IsAbs(promptRoot) {
		return "", errors.New("droid reviewer: absolute task prompt root is required")
	}
	if err := os.MkdirAll(promptRoot, 0o700); err != nil {
		return "", fmt.Errorf("droid reviewer: create settings directory: %w", err)
	}
	settings := map[string]any{
		"sessionDefaultSettings": map[string]any{
			"autonomyLevel": "high",
		},
		"cloudSessionSync":         false,
		"ideAutoConnect":           false,
		"includeCoAuthoredByDroid": false,
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("droid reviewer: encode settings: %w", err)
	}
	path := filepath.Join(promptRoot, settingsFilename)
	if err := hookutil.AtomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("droid reviewer: write settings: %w", err)
	}
	return path, nil
}

func (*Reviewer) ReviewMessage(ctx context.Context, inv ports.ReviewInvocation) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return inv.Prompt, nil
}

func (*Reviewer) ReviewCancel(ctx context.Context) (ports.ReviewCancelSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ReviewCancelSpec{}, err
	}
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 1}, nil
}
