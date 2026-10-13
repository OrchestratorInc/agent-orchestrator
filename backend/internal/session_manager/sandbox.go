package sessionmanager

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sandboxReleaseBudget bounds the shared-container release that follows a
// kill. It runs detached from the kill's own budget, which may be spent.
const sandboxReleaseBudget = 60 * time.Second

// sandboxDiscardBudget bounds removing a failed launch's per-agent container.
const sandboxDiscardBudget = 30 * time.Second

// sandboxLaunch carries one sandboxed launch from preparation to the
// supervisor wrap (#6663). nil means the agent runs on the host.
type sandboxLaunch struct {
	target        ports.SandboxTarget
	workspacePath string
	binary        string
	finish        func()
}

// done ends the launch's in-flight registration. It is nil-safe and
// idempotent so launch paths can defer it unconditionally.
func (sb *sandboxLaunch) done() {
	if sb == nil || sb.finish == nil {
		return
	}
	sb.finish()
	sb.finish = nil
}

// sandboxInFlight counts launches per project between Ensure and the moment
// the session row is live again. A restored session is still terminated while
// its container is ensured, so the shared-container release must also treat
// these launches as users.
type sandboxInFlight struct {
	mu    sync.Mutex
	count map[domain.ProjectID]int
}

func (f *sandboxInFlight) begin(project domain.ProjectID) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.count == nil {
		f.count = map[domain.ProjectID]int{}
	}
	f.count[project]++
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.count[project]--; f.count[project] <= 0 {
				delete(f.count, project)
			}
		})
	}
}

func (f *sandboxInFlight) active(project domain.ProjectID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count[project] > 0
}

func sandboxFor(kind domain.SessionKind, cfg domain.ProjectConfig) domain.SandboxConfig {
	return domain.EffectiveSandbox(cfg.Sandbox, roleOverride(kind, cfg))
}

// prepareSandboxLaunch ensures the container and harness state for a
// sandboxed role. It never falls back to the host on failure. A non-nil
// result is registered in flight until its done is called.
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
	finish := m.sandboxInFlight.begin(rec.ProjectID)
	target, err := m.sandbox.Ensure(ctx, ports.SandboxRequest{
		SessionID: rec.ID, ProjectID: rec.ProjectID, Config: sb,
		WorkspacePath: workspacePath, RequireBinaries: []string{support.SandboxBinary()},
	})
	if err != nil {
		finish()
		return nil, err
	}
	launch := &sandboxLaunch{target: target, workspacePath: workspacePath, binary: support.SandboxBinary(), finish: finish}
	if err := support.PrepareSandbox(env, m.dataDir, target.ProjectDir, workspacePath); err != nil {
		m.discardSandbox(ctx, launch)
		launch.done()
		return nil, err
	}
	return launch, nil
}

// discardSandbox removes a failed launch's per-agent container. Shared
// containers are left to releaseSharedSandbox.
func (m *Manager) discardSandbox(ctx context.Context, sb *sandboxLaunch) {
	if sb == nil || m.sandbox == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxDiscardBudget)
	defer cancel()
	if err := m.sandbox.Discard(ctx, sb.target); err != nil {
		m.logger.Warn("sandbox: discard container after failed launch", "container", sb.target.Container, "error", err)
	}
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
// session remains and no sandboxed launch is in flight. The adapter evaluates
// the in-use check under its project lock, so a concurrent shared Ensure
// cannot slip between the check and the removal. An unreadable session list
// keeps the container: an unknown is never proof that nothing uses it.
func (m *Manager) releaseSharedSandbox(ctx context.Context, project domain.ProjectID) {
	if m.sandbox == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxReleaseBudget)
	defer cancel()
	inUse := func(ctx context.Context) (bool, error) {
		if m.sandboxInFlight.active(project) {
			return true, nil
		}
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
