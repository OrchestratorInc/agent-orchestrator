package tau

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

//go:embed assets/ao_activity.py
var activityExtension string

func extensionPath(workspace string) string {
	return filepath.Join(workspace, ".tau", "ao_activity.py")
}

// GetAgentHooks installs the explicitly loaded workspace-local observer.
func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return fmt.Errorf("tau: hook workspace is required")
	}
	path := extensionPath(cfg.WorkspacePath)
	return writeManaged(path, activityExtension)
}

// EmitsSemanticMessageAcceptance reports canonical accepted user messages.
func (*Plugin) EmitsSemanticMessageAcceptance() bool { return true }

// DeriveActivityState maps only the native observer lifecycle events.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "stop":
		return domain.ActivityIdle, true
	case "agent-start", "user-prompt-submit":
		return domain.ActivityActive, true
	case "session-end":
		return domain.ActivityExited, true
	default:
		return "", false
	}
}
