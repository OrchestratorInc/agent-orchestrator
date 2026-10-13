package sessionmanager

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sandboxReleaseBudget bounds the shared-container release that follows a
// kill. It runs detached from the kill's own budget, which may be spent.
const sandboxReleaseBudget = 60 * time.Second

// sandboxLaunch carries one sandboxed launch from preparation to the
// supervisor wrap (#6663). nil means the agent runs on the host.
type sandboxLaunch struct {
	target        ports.SandboxTarget
	workspacePath string
	binary        string
}

func sandboxFor(kind domain.SessionKind, cfg domain.ProjectConfig) domain.SandboxConfig {
	return domain.EffectiveSandbox(cfg.Sandbox, roleOverride(kind, cfg))
}

// prepareSandboxLaunch ensures the container and harness state for a
// sandboxed role. It never falls back to the host on failure.
func (m *Manager) prepareSandboxLaunch(ctx context.Context, agent ports.Agent, rec domain.SessionRecord, cfg domain.ProjectConfig, workspacePath string, env map[string]string) (*sandboxLaunch, error) {
	sb := sandboxFor(rec.Kind, cfg)
	if !sb.Enabled() {
		return nil, nil
	}
	if m.sandbox == nil {
		return nil, fmt.Errorf("%w: this daemon has no sandbox support", ports.ErrSandboxUnavailable)
	}
	support, ok := agent.(ports.AgentSandboxSupport)
	if !ok {
		return nil, fmt.Errorf("%w: harness %s cannot run in a sandbox yet; set sandbox.mode to off for this role", ports.ErrSandboxMisconfigured, rec.Harness)
	}
	target, err := m.sandbox.Ensure(ctx, ports.SandboxRequest{
		SessionID: rec.ID, ProjectID: rec.ProjectID, Config: sb,
		WorkspacePath: workspacePath, RequireBinaries: []string{support.SandboxBinary()},
	})
	if err != nil {
		return nil, err
	}
	if err := support.PrepareSandbox(env, m.dataDir, target.ProjectDir, workspacePath); err != nil {
		return nil, err
	}
	return &sandboxLaunch{target: target, workspacePath: workspacePath, binary: support.SandboxBinary()}, nil
}

// sandboxArgv swaps the host-resolved harness path for the in-image binary.
func sandboxArgv(argv []string, binary string) []string {
	out := append([]string(nil), argv...)
	if len(out) > 0 && filepath.Base(out[0]) != "env" {
		out[0] = binary
	}
	return out
}

// releaseSharedSandbox removes a project's shared container once no live
// session remains. The adapter evaluates the in-use check under its project
// lock, so a concurrent shared Ensure cannot slip between the check and the
// removal. An unreadable session list keeps the container: an unknown is
// never proof that nothing uses it.
func (m *Manager) releaseSharedSandbox(ctx context.Context, project domain.ProjectID) {
	if m.sandbox == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxReleaseBudget)
	defer cancel()
	inUse := func(ctx context.Context) (bool, error) {
		recs, err := m.store.ListSessions(ctx, project)
		if err != nil {
			m.logger.Warn("sandbox: keep shared container; session list failed", "projectID", project, "error", err)
			return true, err
		}
		for _, r := range recs {
			if !r.IsTerminated {
				return true, nil
			}
		}
		return false, nil
	}
	if err := m.sandbox.Release(ctx, project, inUse); err != nil {
		m.logger.Warn("sandbox: release shared container", "projectID", project, "error", err)
	}
}
