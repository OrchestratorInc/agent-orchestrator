package sessionmanager

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/skillbridge"
)

// skillSyncTimeout bounds the copy, so a slow disk delays a launch rather than
// holding it. What is not copied in time is copied at the next resume.
const skillSyncTimeout = 15 * time.Second

// Agents whose adapter does not say where they find skills, but whose docs do
// (sources in #8932). The rest are taken to load none of the user's skills.
var (
	agentsSkillReaders = []domain.AgentHarness{
		domain.HarnessGemini, domain.HarnessCopilot, domain.HarnessPi, domain.HarnessAmp,
		domain.HarnessCursor, domain.HarnessOpenCode, domain.HarnessOpenCodeV2,
	}
	claudeSkillReaders = []domain.AgentHarness{
		domain.HarnessCursor, domain.HarnessOpenCode, domain.HarnessOpenCodeV2,
	}
	codexSkillReaders = []domain.AgentHarness{domain.HarnessCursor}
)

// SetSkillSharingEnabled supplies the skill sharing setting. While it reports
// true, sessions get the user's skills their agent misses.
func (m *Manager) SetSkillSharingEnabled(enabled func(context.Context) bool) {
	m.skillSharingMu.Lock()
	defer m.skillSharingMu.Unlock()
	m.skillSharing = enabled
}

func (m *Manager) skillSharingEnabled(ctx context.Context) bool {
	m.skillSharingMu.Lock()
	enabled := m.skillSharing
	m.skillSharingMu.Unlock()
	return enabled != nil && enabled(ctx)
}

func skillSupport(ctx context.Context, agent ports.Agent, harness domain.AgentHarness, home string, request ports.AgentSkillRequest) ports.AgentSkillSupport {
	if source, ok := agent.(ports.AgentSkillSource); ok {
		return source.SkillSupport(ctx, request)
	}
	var support ports.AgentSkillSupport
	for folder, readers := range map[string][]domain.AgentHarness{
		".agents": agentsSkillReaders, ".claude": claudeSkillReaders, ".codex": codexSkillReaders,
	} {
		if slices.Contains(readers, harness) {
			support.Native = append(support.Native, filepath.Join(home, folder, "skills"))
		}
	}
	return support
}

// syncSkills prepares the skills a session's agent misses: a folder of copies
// for an adapter that adds one to the launch, and the index that the chat's
// `/` menu and agents without a folder use. Skills are an extra: a failure is
// logged and the session starts without them.
func (m *Manager) syncSkills(ctx context.Context, agent ports.Agent, id domain.SessionID, launchPath string, env map[string]string) {
	if !m.skillSharingEnabled(ctx) {
		m.removeSkills(id)
		return
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil || !found {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	project, err := m.projectSkillRoots(ctx, rec, launchPath)
	if err != nil {
		// The child repositories' skills are unknown, and a user's skill of the
		// same name must not replace one. What an earlier launch prepared had
		// left them out.
		m.logger.Warn("skills: list workspace repositories", "session", id, "err", err)
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, skillSyncTimeout)
	defer cancel()
	support := skillSupport(syncCtx, agent, rec.Harness, home, ports.AgentSkillRequest{
		WorkspacePath: launchPath, Env: env,
		Chat: domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat,
	})
	sources := skillbridge.UserRoots(home)
	native := append(slices.Clone(support.Native), project...)
	// One listing serves the index and the copies.
	catalog := skillbridge.Scan(syncCtx, sources, native)
	m.writeSkillIndex(id, catalog)
	if support.Layout != "" {
		m.copySkills(syncCtx, rec, support.Layout, sources, native, catalog)
	}
}

func (m *Manager) writeSkillIndex(id domain.SessionID, catalog *skillbridge.Catalog) {
	missing, err := catalog.Missing()
	if err == nil {
		err = skillbridge.WriteIndex(m.dataDir, string(id), missing)
	}
	if err != nil {
		m.logger.Warn("skills: index not written", "session", id, "err", err)
	}
}

func (m *Manager) copySkills(ctx context.Context, rec domain.SessionRecord, layout string, sources, native []string, catalog *skillbridge.Catalog) {
	dir := skillbridge.SessionDir(m.dataDir, string(rec.Harness), string(rec.ID))
	if dir == "" {
		return
	}
	result, err := skillbridge.Sync(ctx, skillbridge.Config{
		Dir: dir, Layout: layout, Sources: sources, Native: native, Catalog: catalog,
	})
	if err != nil {
		m.logger.Warn("skills: some are missing", "session", rec.ID, "err", err)
		return
	}
	for _, skipped := range result.Skipped {
		m.logger.Debug("skills: skipped", "session", rec.ID, "path", skipped.Path, "reason", skipped.Reason)
	}
	m.logger.Info("skills: ready", "session", rec.ID, "harness", rec.Harness,
		"skills", len(result.Skills), "copied", result.Copied, "skipped", len(result.Skipped))
}

// projectSkillRoots are the session's own repositories' skill folders. An
// imported chat can start below the workspace its child repositories are in.
func (m *Manager) projectSkillRoots(ctx context.Context, rec domain.SessionRecord, launchPath string) ([]string, error) {
	workspace := rec.Metadata.WorkspacePath
	if workspace == "" {
		workspace = launchPath
	}
	roots := skillbridge.ProjectRoots(launchPath)
	if workspace != launchPath {
		roots = append(roots, skillbridge.ProjectRoots(workspace)...)
	}
	if rec.ProjectID == "" {
		return roots, nil
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, string(rec.ProjectID))
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		roots = append(roots, skillbridge.ProjectRoots(filepath.Join(workspace, repo.RelativePath))...)
	}
	return roots, nil
}

// skillIndexPrompt points an agent that cannot take a folder of skills at the
// list of those it misses. It is a pointer, not the list: some users have
// hundreds. It lists nothing itself: the instructions are built before the
// launch writes the list, and the pointer says that a missing file means no
// more skills.
func (m *Manager) skillIndexPrompt(ctx context.Context, id domain.SessionID, chat bool) string {
	if !m.skillSharingEnabled(ctx) {
		return ""
	}
	document := skillbridge.IndexDocument(m.dataDir, string(id))
	home, err := os.UserHomeDir()
	if document == "" || err != nil {
		return ""
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil || !found {
		return ""
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return ""
	}
	support := skillSupport(ctx, agent, rec.Harness, home, ports.AgentSkillRequest{Chat: chat})
	if support.Layout != "" || readsEveryUserFolder(support.Native, home) {
		return ""
	}
	return "## User skills\n\n" +
		"The user keeps skills: instructions for particular kinds of task. `" + filepath.ToSlash(document) + "` lists those your own skill list does not have, with what each is for. " +
		"When the user names one, for example as `/name`, or a task clearly matches one, read that list, then the skill's SKILL.md, and follow it. " +
		"A skill of the same name in the project comes first. If the file does not exist, there are no more skills."
}

// readsEveryUserFolder reports whether an agent loads all of the user's skill
// folders itself, so it can miss none (Cursor). It compares folders and lists
// no skills.
func readsEveryUserFolder(native []string, home string) bool {
	for _, folder := range skillbridge.UserRoots(home) {
		if !slices.Contains(native, folder) {
			return false
		}
	}
	return true
}

func (m *Manager) removeSkills(id domain.SessionID) {
	if err := skillbridge.Remove(m.dataDir, string(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		m.logger.Warn("skills: cleanup failed", "session", id, "err", err)
	}
}
