package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
)

// warmInstalledHarnessUpdates runs only after the startup readiness passes,
// using their cached installation observations rather than probing every
// adapter again. The HTTP endpoint can start the same check earlier; the
// installer service shares in-flight work and cached results with this pass.
func warmInstalledHarnessUpdates(
	ctx context.Context,
	readinessDone <-chan struct{},
	readiness func(context.Context) (agentsvc.Readiness, error),
	check func(context.Context, systeminstall.Target) (systeminstall.UpdateAdvisory, error),
	log *slog.Logger,
) {
	select {
	case <-readinessDone:
	case <-ctx.Done():
		return
	}
	if ctx.Err() != nil {
		return
	}
	known, err := readiness(ctx)
	if err != nil {
		log.Warn("harness update warm-up skipped: readiness unavailable", "err", err)
		return
	}
	for _, agent := range known.Agents {
		if ctx.Err() != nil {
			return
		}
		target := systeminstall.Target(agent.ID)
		if agent.Installation.State != domain.AgentInstallationInstalled || !systeminstall.IsAgentTarget(target) {
			continue
		}
		if _, err := check(ctx, target); err != nil && ctx.Err() == nil {
			log.Warn("harness update warm-up failed", "agent", agent.ID, "err", err)
		}
	}
}
