package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/attribution"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SetCommitAttributionEnabled supplies the Orchestrator attribution beta
// switch. While it reports true, new sessions credit Orchestrator on their
// commits and PRs (unless the project turns a part off); sessions already
// running keep what they started with.
func (m *Manager) SetCommitAttributionEnabled(enabled func(context.Context) bool) {
	m.commitAttributionMu.Lock()
	defer m.commitAttributionMu.Unlock()
	m.commitAttribution = enabled
}

func (m *Manager) commitAttributionEnabled(ctx context.Context) bool {
	m.commitAttributionMu.Lock()
	enabled := m.commitAttribution
	m.commitAttributionMu.Unlock()
	return enabled != nil && enabled(ctx)
}

// commitTrailerWanted reports whether new commits in this project's sessions
// get the Co-authored-by: Orchestrator trailer.
func (m *Manager) commitTrailerWanted(ctx context.Context, project domain.ProjectRecord) bool {
	return !project.Config.Attribution.DisableCommitTrailer && m.commitAttributionEnabled(ctx)
}

// prFooterWanted reports whether agents in this project end PR bodies with the
// Orchestrator footer. Projects without a remote have no PRs to sign; in a
// workspace project the remotes may live only on its child repositories.
func (m *Manager) prFooterWanted(ctx context.Context, project domain.ProjectRecord) bool {
	if project.Config.Attribution.DisablePRFooter || !m.commitAttributionEnabled(ctx) {
		return false
	}
	if strings.TrimSpace(project.RepoOriginURL) != "" {
		return true
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return false
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		m.logger.Warn("attribution: list workspace repos", "project", project.ID, "error", err)
		return false
	}
	for _, repo := range repos {
		if strings.TrimSpace(repo.RepoOriginURL) != "" {
			return true
		}
	}
	return false
}

// attributionChoice says which choice applyCommitAttribution applies.
type attributionChoice int

const (
	// attributionFromSettings is for a session starting now, in a new or a
	// reused worktree: today's beta and project switches decide, and the
	// choice is recorded on each worktree's branch.
	attributionFromSettings attributionChoice = iota
	// attributionRecorded is for an existing session whose reclaimed worktree
	// was recreated: it keeps the choice it started with.
	attributionRecorded
)

// applyCommitAttribution routes a session's git hooks through the Orchestrator
// dispatcher and sets the trailer it adds, or undoes that in a reused worktree
// an earlier session configured. With attribution never turned on (no hooks
// dir) it runs no git at all.
//
// A workspace project's root is a worktree of the root repository, and each
// child repository has its own worktree below it; every one is configured.
// Failures are logged, never fatal: a missing trailer must not stop a session
// from starting.
func (m *Manager) applyCommitAttribution(ctx context.Context, project domain.ProjectRecord, workspacePath string, choice attributionChoice) {
	if workspacePath == "" {
		return
	}
	hooksDir := attribution.HooksDir(m.dataDir)
	coAuthor := ""
	if choice == attributionFromSettings && m.commitTrailerWanted(ctx, project) {
		coAuthor = attribution.Bot.CoAuthor()
	}
	if coAuthor == "" {
		if _, err := os.Stat(hooksDir); err != nil {
			return
		}
	}
	installed := false
	for _, worktree := range m.sessionGitWorktrees(ctx, project, workspacePath) {
		want := coAuthor
		if choice == attributionRecorded {
			want = attribution.RecordedChoice(ctx, worktree)
		}
		if want == "" {
			if attribution.ConfiguredFor(ctx, worktree, hooksDir) {
				if err := attribution.RemoveFromWorktree(ctx, worktree); err != nil {
					m.logger.Warn("attribution: remove from worktree", "path", worktree, "error", err)
				}
			}
		} else {
			if !installed {
				if err := attribution.InstallHooks(hooksDir); err != nil {
					m.logger.Warn("attribution: install git hooks", "error", err)
					return
				}
				installed = true
			}
			if err := attribution.ConfigureWorktree(ctx, worktree, hooksDir, want); err != nil {
				m.logger.Warn("attribution: configure worktree", "path", worktree, "error", err)
				continue
			}
		}
		if choice == attributionFromSettings {
			if err := attribution.RecordChoice(ctx, worktree, want); err != nil {
				m.logger.Warn("attribution: record choice", "path", worktree, "error", err)
			}
		}
	}
}

// sessionGitWorktrees lists the linked git worktrees of a session workspace:
// the workspace itself, plus each child repository's worktree for a workspace
// project. Only linked worktrees (a .git file, not a directory) are listed, so
// a person's own checkout is never touched.
func (m *Manager) sessionGitWorktrees(ctx context.Context, project domain.ProjectRecord, root string) []string {
	var out []string
	if isLinkedWorktree(root) {
		out = append(out, root)
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return out
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		m.logger.Warn("attribution: list workspace repos", "project", project.ID, "error", err)
		return out
	}
	for _, repo := range repos {
		child := filepath.Join(root, filepath.FromSlash(repo.RelativePath))
		if child != root && isLinkedWorktree(child) {
			out = append(out, child)
		}
	}
	return out
}

func isLinkedWorktree(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// prAttributionPrompt asks the agent to end every PR body with the footer.
// Desktop agents open PRs with the person's own gh login, so the prompt is
// where the footer is added.
func prAttributionPrompt() string {
	return "## Orchestrator Attribution\n\n" +
		"- When you create a PR/MR, end its body with this exact line, after a blank line, and keep it when you edit the body later: " +
		"`" + attribution.PRFooter + "`\n" +
		"- Add it once. Do not add it to commit messages, issues, or comments.\n" +
		"- Keep any other co-author lines or footers your own tools add."
}
