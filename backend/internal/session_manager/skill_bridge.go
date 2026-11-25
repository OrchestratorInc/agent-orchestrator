package sessionmanager

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillbridge"
)

// skillBridgeTimeout bounds the copy, so a slow disk delays a launch rather
// than holding it. What is not copied in time is copied at the next resume.
const skillBridgeTimeout = 15 * time.Second

// SetSkillBridge supplies the skill bridge switch (Developer mode). While it is
// on, sessions get a private copy of the user's skills their agent misses.
func (m *Manager) SetSkillBridge(policy func() (enabled, known bool)) {
	m.skillBridgeMu.Lock()
	defer m.skillBridgeMu.Unlock()
	m.skillBridge = policy
}

// skillBridgeWanted reports whether a session gets bridged skills. Until the
// desktop says whether Developer mode is on, a session keeps what it had: one
// resumed as the daemon boots must not lose its skills.
func (m *Manager) skillBridgeWanted(dir string) bool {
	m.skillBridgeMu.Lock()
	policy := m.skillBridge
	m.skillBridgeMu.Unlock()
	if policy == nil {
		return false
	}
	enabled, known := policy()
	if known {
		return enabled
	}
	_, err := os.Stat(dir)
	return err == nil
}

// syncSkillBridge prepares the folder of skills the session's agent adapter
// adds to its launch. Skills are an extra: a failure is logged and the session
// starts without them.
func (m *Manager) syncSkillBridge(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, env map[string]string) {
	bridge, ok := agent.(ports.AgentSkillBridge)
	if !ok {
		return
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil || !found {
		return
	}
	dir := skillbridge.SessionDir(m.dataDir, string(rec.Harness), string(id))
	if dir == "" {
		return
	}
	if !m.skillBridgeWanted(dir) {
		m.removeSkillBridge(id)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	native := append(bridge.NativeSkillRoots(ctx, workspacePath, env), skillbridge.ProjectRoots(workspacePath)...)
	repos, err := m.store.ListWorkspaceRepos(ctx, string(rec.ProjectID))
	if err != nil {
		m.logger.Warn("skill bridge: list workspace repositories", "session", id, "err", err)
	}
	for _, repo := range repos {
		native = append(native, skillbridge.ProjectRoots(filepath.Join(workspacePath, repo.RelativePath))...)
	}
	syncCtx, cancel := context.WithTimeout(ctx, skillBridgeTimeout)
	defer cancel()
	result, err := skillbridge.Sync(syncCtx, skillbridge.Config{
		Dir: dir, Sources: skillbridge.UserRoots(home), Native: native,
	})
	if err != nil {
		m.logger.Warn("skill bridge: some skills are missing", "session", id, "err", err)
	}
	for _, skipped := range result.Skipped {
		m.logger.Debug("skill bridge: skill skipped", "session", id, "path", skipped.Path, "reason", skipped.Reason)
	}
	m.logger.Info("skill bridge: skills ready", "session", id, "harness", rec.Harness,
		"skills", len(result.Skills), "copied", result.Copied, "skipped", len(result.Skipped))
}

func (m *Manager) removeSkillBridge(id domain.SessionID) {
	if err := skillbridge.Remove(m.dataDir, string(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		m.logger.Warn("skill bridge cleanup failed", "session", id, "err", err)
	}
}
