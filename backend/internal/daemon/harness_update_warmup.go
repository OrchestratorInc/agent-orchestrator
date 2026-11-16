package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
)

// watchInstalledHarnessUpdates checks installed harnesses for updates once
// the startup readiness passes, then again every interval for as long as the
// daemon runs, so a long-lived daemon notices new releases without a restart.
// Each pass first refreshes installation observations older than the display
// policy (binary lookups only, no authentication checks), so it covers
// harnesses installed outside AO since the last pass. The first pass runs just
// after warm-up and reuses those observations. The first pass shares in-flight work and cached results with the HTTP
// endpoint. Later passes force a fresh lookup: a result cached just under one
// interval ago would otherwise be served again for another interval. A pass
// submits every installed harness at once; the installer service runs at most
// two checks at a time, so a pass costs about two CLI version probes of memory.
func watchInstalledHarnessUpdates(
	ctx context.Context,
	readinessDone <-chan struct{},
	interval time.Duration,
	readiness func(context.Context) (agentsvc.Readiness, error),
	check func(ctx context.Context, target systeminstall.Target, refresh bool) (systeminstall.UpdateAdvisory, error),
	log *slog.Logger,
) {
	select {
	case <-readinessDone:
	case <-ctx.Done():
		return
	}
	checkInstalledHarnessUpdates(ctx, false, readiness, check, log)
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkInstalledHarnessUpdates(ctx, true, readiness, check, log)
		}
	}
}

func checkInstalledHarnessUpdates(
	ctx context.Context,
	refresh bool,
	readiness func(context.Context) (agentsvc.Readiness, error),
	check func(ctx context.Context, target systeminstall.Target, refresh bool) (systeminstall.UpdateAdvisory, error),
	log *slog.Logger,
) {
	if ctx.Err() != nil {
		return
	}
	known, err := readiness(ctx)
	if err != nil {
		log.Warn("harness update check skipped: readiness unavailable", "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, agent := range known.Agents {
		target := systeminstall.Target(agent.ID)
		if agent.Installation.State != domain.AgentInstallationInstalled || !systeminstall.IsAgentTarget(target) {
			continue
		}
		wg.Go(func() {
			defer sentryobs.RecoverBackground()
			if _, err := check(ctx, target, refresh); err != nil && ctx.Err() == nil {
				log.Warn("harness update check failed", "agent", agent.ID, "err", err)
			}
		})
	}
	wg.Wait()
}
