package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/gitdefault"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SpawnPreflightInput names the spawn a caller is about to request. Empty
// Harness and Mode resolve exactly as Spawn would resolve them.
type SpawnPreflightInput struct {
	ProjectID   domain.ProjectID
	Kind        domain.SessionKind
	Harness     domain.AgentHarness
	Mode        domain.SessionMode
	Permissions ports.PermissionMode
}

// SpawnPreflightFacts is what Spawn would decide before creating durable
// state, without creating any. Harness is empty when neither the request nor
// the project's role override names one.
type SpawnPreflightFacts struct {
	Harness domain.AgentHarness
	Mode    domain.SessionMode
	// DefaultBranchUnresolved is true when a repository the spawn would seed a
	// worktree from has no authoritative default branch.
	DefaultBranchUnresolved bool
}

// PreflightSpawn resolves the harness, controller mode, and default branch the
// same way Spawn does, so pre-flight cannot pass a spawn that would fail on
// those facts. It never creates a session, worktree, or runtime. On a
// default-branch probe error the harness and mode facts are still returned.
func (m *Manager) PreflightSpawn(ctx context.Context, in SpawnPreflightInput) (SpawnPreflightFacts, error) {
	project, err := m.loadProject(ctx, in.ProjectID)
	if err != nil {
		return SpawnPreflightFacts{}, err
	}
	if in.ProjectID != "" && project.ID == "" {
		return SpawnPreflightFacts{}, fmt.Errorf("preflight: %w", ErrProjectNotResolvable)
	}
	facts := SpawnPreflightFacts{Harness: effectiveHarness(in.Harness, in.Kind, project.Config)}
	if facts.Harness != "" {
		facts.Mode = m.preflightMode(ctx, in, facts.Harness, project.Config)
	}
	// Harness and Mode stay valid when the branch probe fails, so a caller can
	// treat a timed-out probe as unknown and still check the harness.
	facts.DefaultBranchUnresolved, err = m.defaultBranchUnresolved(ctx, project)
	return facts, err
}

// preflightMode mirrors Spawn's controller-mode choice, including the
// best-effort fallback from an inherited Chat default to TUI, because only a
// TUI launch needs the terminal runtime prerequisites.
func (m *Manager) preflightMode(ctx context.Context, in SpawnPreflightInput, harness domain.AgentHarness, cfg domain.ProjectConfig) domain.SessionMode {
	requested := in.Mode
	if harness == domain.HarnessUnreal && !requested.Valid() {
		requested = domain.SessionModeChat
	}
	mode := m.resolveSessionMode(ctx, in.Kind, requested)
	if mode != domain.SessionModeChat || requested.Valid() {
		return mode
	}
	if m.chat == nil {
		return domain.SessionModeTUI
	}
	permissions := in.Permissions
	if permissions == "" {
		permissions = effectiveAgentConfig(harness, in.Kind, cfg).Permissions
	}
	if err := m.chat.PreflightChat(ctx, harness, permissions); err != nil &&
		(errors.Is(err, ports.ErrChatUnsupported) ||
			errors.Is(err, ports.ErrChatDriverUnavailable) ||
			errors.Is(err, ports.ErrChatDriverIncompatible) ||
			errors.Is(err, ports.ErrChatAuthRequired)) {
		return domain.SessionModeTUI
	}
	return mode
}

// defaultBranchUnresolved resolves each repository a project spawn seeds from,
// using the same adapter call as the spawn's default-branch refresh. Only an
// authoritative "no default" answer counts; a failed or cancelled probe is not
// proof the spawn will fail.
func (m *Manager) defaultBranchUnresolved(ctx context.Context, project domain.ProjectRecord) (bool, error) {
	if project.ID == "" || project.Kind.WithDefault() == domain.ProjectKindScratch || strings.TrimSpace(project.Path) == "" {
		return false, nil
	}
	refresher, ok := m.workspace.(ports.WorkspaceDefaultBranchRefresher)
	if !ok {
		return false, nil
	}
	targets := []defaultBranchRefreshTarget{{repoPath: project.Path, configuredBranch: project.Config.WorktreeBaseBranch()}}
	if project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
		if err != nil {
			return false, fmt.Errorf("preflight: list workspace repos: %w", err)
		}
		for _, repo := range repos {
			if repo.GitStatus == domain.GitStatusNeedsInit {
				continue
			}
			targets = append(targets, defaultBranchRefreshTarget{
				repoPath:         filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath)),
				configuredBranch: repo.DefaultBranch,
			})
		}
	}
	for _, target := range targets {
		_, err := refresher.ResolveDefaultBranch(ctx, target.repoPath, target.configuredBranch)
		if errors.Is(err, gitdefault.ErrUnresolved) || errors.Is(err, ports.ErrWorkspaceDefaultBranchUnresolved) {
			return true, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	return false, nil
}
