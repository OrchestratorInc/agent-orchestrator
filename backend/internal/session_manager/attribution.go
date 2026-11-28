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

// prFooterWanted reports whether agents in this project end PR bodies with the
// Orchestrator footer. Projects without a remote have no PRs to sign; in a
// workspace project the remotes may live only on its child repositories.
func (m *Manager) prFooterWanted(ctx context.Context, project domain.ProjectRecord) bool {
	want, _ := m.prFooterChoice(ctx, project)
	return want
}

// prFooterChoice is prFooterWanted plus whether the answer is settled: false
// when a workspace's repositories could not be listed, so the footer is left
// out for now but no choice is recorded for the session.
func (m *Manager) prFooterChoice(ctx context.Context, project domain.ProjectRecord) (want, known bool) {
	return m.prFooterChoiceWith(ctx, project, m.commitAttributionEnabled(ctx))
}

// prFooterChoiceWith is prFooterChoice with the beta already read.
func (m *Manager) prFooterChoiceWith(ctx context.Context, project domain.ProjectRecord, enabled bool) (want, known bool) {
	if project.Config.Attribution.DisablePRFooter || !enabled {
		return false, true
	}
	if strings.TrimSpace(project.RepoOriginURL) != "" {
		return true, true
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return false, true
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		m.logger.Warn("attribution: list workspace repos", "project", project.ID, "error", err)
		return false, false
	}
	for _, repo := range repos {
		if strings.TrimSpace(repo.RepoOriginURL) != "" {
			return true, true
		}
	}
	return false, true
}

// attributionChoice says which choice applyCommitAttribution applies.
type attributionChoice int

const (
	// attributionFromSettings is for a session starting now, in a new or a
	// reused worktree: today's beta and project switches decide, and a
	// trailer is recorded for the session.
	attributionFromSettings attributionChoice = iota
	// attributionRecorded is for an existing session whose reclaimed worktree
	// was recreated: it keeps the choice it started with.
	attributionRecorded
	// attributionDecided is for a spawn's own setup: it applies the decision
	// the spawn made before writing the session's first prompt.
	attributionDecided
)

// spawnAttribution is the trailer decision a spawn made before its first
// prompt. The spawn carries it to its workspace setup itself, so no other
// spawn, retry or settings change can replace it on the way.
type spawnAttribution struct {
	coAuthor string
	decided  bool
}

// recordSpawnAttribution decides, once per spawn and before the session's
// first prompt is written, whether its commits get the trailer and its PRs the
// footer, and records both. The prompt, the database and the worktree's hook
// config then all follow this one decision, whatever the settings say later.
// A retry decides again, so a choice from an attempt made before the settings
// changed is not kept.
func (m *Manager) recordSpawnAttribution(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID) spawnAttribution {
	if m.store == nil || projectID == "" || sessionID == "" {
		return spawnAttribution{}
	}
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		m.logger.Warn("attribution: load project", "project", projectID, "error", err)
		return spawnAttribution{}
	}
	return spawnAttribution{coAuthor: m.recordSpawnAttributionFor(ctx, project, sessionID), decided: true}
}

// recordSpawnAttributionFor decides the trailer and footer for a starting
// session from the current settings, records both, and returns the trailer.
// The spawn hands that return value to its own workspace setup, which
// applies it whether or not recording it succeeded, so neither a failed
// write nor a row left by an earlier attempt can change it.
func (m *Manager) recordSpawnAttributionFor(ctx context.Context, project domain.ProjectRecord, sessionID domain.SessionID) string {
	// The beta is read once, so one toggle cannot give the trailer and the
	// footer different answers.
	enabled := m.commitAttributionEnabled(ctx)
	coAuthor := ""
	if enabled && !project.Config.Attribution.DisableCommitTrailer {
		coAuthor = attribution.Bot.CoAuthor()
	}
	if sessionID == "" {
		return coAuthor
	}
	if m.store != nil {
		// Recorded even without a trailer, which also clears one an earlier
		// attempt recorded, so a recreated worktree gets this decision.
		if err := m.store.SetSessionCommitAttribution(ctx, sessionID, coAuthor); err != nil {
			m.logger.Warn("attribution: record session choice", "sessionID", sessionID, "error", err)
		}
	}
	m.recordPRFooterChoice(ctx, project, sessionID, enabled)
	return coAuthor
}

// recordPRFooterChoice keeps whether a starting session's agents sign PRs, so
// its prompt says the same when it is rebuilt for a relaunch, a restore or a
// switch between Chat and the terminal, whatever the settings say by then.
func (m *Manager) recordPRFooterChoice(ctx context.Context, project domain.ProjectRecord, sessionID domain.SessionID, enabled bool) {
	if m.store == nil || sessionID == "" {
		return
	}
	footer, known := m.prFooterChoiceWith(ctx, project, enabled)
	if !known {
		// Not settled: later prompts follow the settings until a lookup works.
		return
	}
	if err := m.store.SetSessionPRFooter(ctx, sessionID, footer); err != nil {
		m.logger.Warn("attribution: record PR footer choice", "sessionID", sessionID, "error", err)
	}
}

// sessionPRFooter reports whether this session's prompt asks for the PR
// footer: the choice it started with, or the current settings for a session
// with none recorded (one still starting, or one from before choices were kept).
func (m *Manager) sessionPRFooter(ctx context.Context, project domain.ProjectRecord, sessionID domain.SessionID) bool {
	if m.store != nil && sessionID != "" {
		footer, recorded, err := m.store.SessionPRFooter(ctx, sessionID)
		if err != nil {
			m.logger.Warn("attribution: read PR footer choice", "sessionID", sessionID, "error", err)
		} else if recorded {
			return footer
		}
	}
	return m.prFooterWanted(ctx, project)
}

// applyCommitAttribution routes a session's git hooks through the Orchestrator
// dispatcher and sets the trailer it adds, or undoes that in a reused worktree
// an earlier session configured. With attribution never turned on (no hooks
// dir) it runs no git at all.
//
// A workspace project's root is a worktree of the root repository, and each
// child repository has its own worktree below it; every one is configured.
// Failures are logged, never fatal: a missing trailer must not stop a session
// from starting.
func (m *Manager) applyCommitAttribution(ctx context.Context, project domain.ProjectRecord, sessionID domain.SessionID, workspacePath string, choice attributionChoice) string {
	if workspacePath == "" {
		return ""
	}
	coAuthor := ""
	switch choice {
	case attributionFromSettings:
		// A session set up without a spawn-time decision (an import) decides
		// now, and records it like a spawn.
		coAuthor = m.recordSpawnAttributionFor(ctx, project, sessionID)
	case attributionRecorded:
		recorded, err := m.store.SessionCommitAttribution(ctx, sessionID)
		if err != nil {
			m.logger.Warn("attribution: read session choice", "sessionID", sessionID, "error", err)
		}
		coAuthor = recorded
	}
	return m.configureAttribution(ctx, project, workspacePath, coAuthor)
}

// configureAttribution routes a workspace's git worktrees through the
// dispatcher with trailer coAuthor, or undoes an earlier session's setup when
// coAuthor is empty, and returns coAuthor.
func (m *Manager) configureAttribution(ctx context.Context, project domain.ProjectRecord, workspacePath, coAuthor string) string {
	if workspacePath == "" {
		return ""
	}
	base := attribution.HooksDir(m.dataDir)
	if coAuthor == "" {
		if _, err := os.Stat(base); err != nil {
			return ""
		}
	}
	for _, worktree := range m.sessionGitWorktrees(ctx, project, workspacePath) {
		if coAuthor == "" {
			if attribution.ConfiguredFor(ctx, worktree, base) {
				if err := attribution.RemoveFromWorktree(ctx, worktree); err != nil {
					m.logger.Warn("attribution: remove from worktree", "path", worktree, "error", err)
				}
			}
			continue
		}
		if err := attribution.ConfigureWorktree(ctx, worktree, base, coAuthor); err != nil {
			m.logger.Warn("attribution: configure worktree", "path", worktree, "error", err)
		}
	}
	return coAuthor
}

// refreshAttributionHooks runs after project setup. It rescans the configured
// worktrees of a session for on-demand repository hooks (reference-transaction
// and the like) that setup installed, so they still run: without the
// dispatcher for its name git would skip one, since core.hooksPath points at
// Orchestrator's dir. Setup can also point a worktree's core.hooksPath at the
// repository's own hooks (a hook manager does); a session with the trailer
// (coAuthor) is then configured again, which saves that path so the
// dispatcher still runs those hooks.
func (m *Manager) refreshAttributionHooks(ctx context.Context, project domain.ProjectRecord, workspacePath, coAuthor string) {
	hooksDir := attribution.HooksDir(m.dataDir)
	if workspacePath == "" || len(project.Config.PostCreate) == 0 {
		return
	}
	if _, err := os.Stat(hooksDir); err != nil {
		return
	}
	for _, worktree := range m.sessionGitWorktrees(ctx, project, workspacePath) {
		if !attribution.ConfiguredFor(ctx, worktree, hooksDir) {
			if coAuthor == "" {
				continue
			}
			if err := attribution.ConfigureWorktree(ctx, worktree, hooksDir, coAuthor); err != nil {
				m.logger.Warn("attribution: configure worktree after setup", "path", worktree, "error", err)
			}
			continue
		}
		if err := attribution.RefreshRepositoryHooks(ctx, worktree, hooksDir); err != nil {
			m.logger.Warn("attribution: refresh repository hooks", "path", worktree, "error", err)
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
