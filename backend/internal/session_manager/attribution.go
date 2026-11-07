package sessionmanager

import (
	"context"
	"io/fs"
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
// Orchestrator footer. Projects without a remote have no PRs to sign.
func (m *Manager) prFooterWanted(ctx context.Context, project domain.ProjectRecord) bool {
	return strings.TrimSpace(project.RepoOriginURL) != "" &&
		!project.Config.Attribution.DisablePRFooter && m.commitAttributionEnabled(ctx)
}

// applyCommitAttribution routes a session workspace's git hooks through the
// Orchestrator dispatcher and sets the trailer it adds. It runs only while the
// beta is on and the project keeps the trailer; with either off it changes
// nothing, so turning the beta off leaves new sessions exactly as before.
//
// A workspace project's root is not a repository; each child repository
// worktree below it is configured instead. Failures are logged, never fatal:
// a missing trailer must not stop a session from starting.
func (m *Manager) applyCommitAttribution(ctx context.Context, project domain.ProjectRecord, workspacePath string) {
	if workspacePath == "" || !m.commitTrailerWanted(ctx, project) {
		return
	}
	hooksDir := attribution.HooksDir(m.dataDir)
	if err := attribution.InstallHooks(hooksDir); err != nil {
		m.logger.Warn("attribution: install git hooks", "error", err)
		return
	}
	for _, worktree := range sessionGitWorktrees(workspacePath) {
		if err := attribution.ConfigureWorktree(ctx, worktree, hooksDir, attribution.Bot.CoAuthor()); err != nil {
			m.logger.Warn("attribution: configure worktree", "path", worktree, "error", err)
		}
	}
}

// sessionGitWorktrees lists the linked git worktrees a session workspace holds:
// the workspace itself for a repository project, or the child repositories of
// a workspace project. Linked worktrees have a .git file, not a directory, so a
// person's own checkout nested in the tree is never touched. The walk stays
// shallow and skips dependency and hidden directories.
func sessionGitWorktrees(root string) []string {
	if isLinkedWorktree(root) {
		return []string{root}
	}
	const maxDepth = 4
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory holds no worktree we can configure.
			return filepath.SkipDir
		}
		if !d.IsDir() || path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		name := d.Name()
		if relErr != nil || strings.Count(rel, string(filepath.Separator)) >= maxDepth ||
			strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			return filepath.SkipDir
		}
		if isLinkedWorktree(path) {
			out = append(out, path)
			return filepath.SkipDir
		}
		return nil
	})
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
