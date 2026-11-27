package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func (e *Engine) projectRules(ctx context.Context, id domain.ProjectID) (string, error) {
	if e.projects == nil {
		return "", nil
	}
	project, ok, err := e.projects.GetProject(ctx, string(id))
	if err != nil || !ok {
		return "", err
	}

	rules := strings.TrimSpace(project.Config.AgentRules)
	path := strings.TrimSpace(project.Config.AgentRulesFile)
	if path == "" {
		return rules, nil
	}
	if e.ruleFiles == nil {
		return "", fmt.Errorf("agentRulesFile %q: no project rules file reader", path)
	}
	body, err := e.ruleFiles.ReadDefaultBranchFile(ctx, project.Path, project.Config.WorktreeBaseBranch(), path)
	if err != nil {
		return "", fmt.Errorf("agentRulesFile %q: read from the project default branch: %w", path, err)
	}
	if rules == "" {
		return strings.TrimSpace(body), nil
	}
	if body = strings.TrimSpace(body); body == "" {
		return rules, nil
	}
	return rules + "\n\n" + body, nil
}
