package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/forkseed"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

type forkSeedApplier interface {
	Apply(ctx context.Context, worktree, ref string) error
	Delete(ctx context.Context, workspace, ref string) error
	RecordBase(ctx context.Context, worktree, ref string) error
	UnchangedSince(ctx context.Context, worktree, ref string) (bool, error)
	DiscardChanges(ctx context.Context, worktree string) error
	RestoreBase(ctx context.Context, worktree, ref string) (bool, error)
}

type gitForkSeeds struct{}

func (gitForkSeeds) Apply(ctx context.Context, worktree, ref string) error {
	return forkseed.Apply(ctx, worktree, ref)
}

func (gitForkSeeds) Delete(ctx context.Context, workspace, ref string) error {
	return forkseed.Delete(ctx, workspace, ref)
}

func (gitForkSeeds) RecordBase(ctx context.Context, worktree, ref string) error {
	return forkseed.RecordBase(ctx, worktree, ref)
}

func (gitForkSeeds) UnchangedSince(ctx context.Context, worktree, ref string) (bool, error) {
	return forkseed.UnchangedSince(ctx, worktree, ref)
}

func (gitForkSeeds) RestoreBase(ctx context.Context, worktree, ref string) (bool, error) {
	return forkseed.RestoreBase(ctx, worktree, ref)
}

func (gitForkSeeds) DiscardChanges(ctx context.Context, worktree string) error {
	return forkseed.DiscardChanges(ctx, worktree)
}

func (m *Manager) createForkWorkspace(ctx context.Context, fork *ports.SpawnFork, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, *ports.WorkspaceProjectInfo, error) {
	if fork.StartPoint == "" {
		return ports.WorkspaceInfo{}, nil, errors.New("session fork: missing start point")
	}
	cfg.FreshBranch = true
	cfg.StartPoint = fork.StartPoint
	ws, err := m.workspace.Create(ctx, cfg)
	if err != nil || fork.SeedRef == "" {
		return ws, nil, err
	}
	applyErr := m.forkSeeds.Apply(ctx, ws.Path, fork.SeedRef)
	if !fork.KeepSeedRef {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = m.forkSeeds.Delete(cleanupCtx, ws.Path, fork.SeedRef)
	}
	if applyErr != nil {
		return ws, nil, fmt.Errorf("session fork: copy source files: %w", applyErr)
	}
	m.markForkWorkspace(ws.Path)
	if err := m.forkSeeds.RecordBase(ctx, ws.Path, forkseed.BaseRef(string(cfg.SessionID))); err != nil {
		m.logger.Warn("session fork: record the fork's starting files", "workspace", ws.Path, "err", err)
	}
	if err := m.restoreAttachments(ctx, cfg.SessionID, ws); err != nil {
		m.logger.Warn("session fork: place attachments in the fork", "workspace", ws.Path, "err", err)
	}
	if fork.SourceWorkspace != "" {
		cloned, err := cloneForkDependencies(ctx, fork.SourceWorkspace, ws.Path)
		if err != nil && !errors.Is(err, errCloneUnsupported) {
			m.logger.Warn("session fork: clone dependencies; setup will install them", "workspace", ws.Path, "err", err)
		} else if len(cloned) > 0 {
			m.logger.Info("session fork: cloned dependencies", "workspace", ws.Path, "folders", cloned)
		}
	}
	return ws, nil, nil
}

func (m *Manager) copyForkFiles(ctx context.Context, fork *ports.SpawnFork, id domain.SessionID, artifactDir string) error {
	if artifactDir != "" {
		var err error
		if fork.ArtifactsAsOf.IsZero() && fork.ArtifactsTurnID == "" {
			err = m.copyForkArtifacts(ctx, fork.SourceSessionID, artifactDir)
		} else {
			err = m.copyForkArtifactsAsOf(ctx, fork.SourceSessionID, artifactDir, fork.ArtifactsAsOf, fork.ArtifactsTurnID)
		}
		if err != nil {
			return err
		}
	}
	return m.copyForkAttachments(ctx, fork, id)
}

func (m *Manager) copyForkArtifacts(ctx context.Context, source domain.SessionID, dst string) error {
	if source == "" {
		return nil
	}
	if err := copyTree(ctx, m.artifactDir(source), dst, sessionartifacts.VersionsDir, nil); err != nil {
		return fmt.Errorf("copy artifacts of %s: %w", source, err)
	}
	return nil
}

type artifactVersionLister interface {
	ListSessionArtifactVersions(ctx context.Context, id domain.SessionID, path string) ([]domain.SessionArtifactVersion, error)
	ListLatestSessionArtifactVersions(ctx context.Context, id domain.SessionID) ([]domain.SessionArtifactVersion, error)
}

func (m *Manager) copyForkArtifactsAsOf(ctx context.Context, source domain.SessionID, dst string, asOf time.Time, turnID string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	src := m.artifactDir(source)
	if turnID != "" {
		files, found, err := sessionartifacts.TurnArtifacts(src, turnID)
		if err != nil {
			return err
		}
		if found {
			return copyTurnArtifacts(ctx, src, dst, files)
		}
	}
	lister, _ := m.store.(artifactVersionLister)
	paths := map[string]bool{}
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if entry.IsDir() && rel == sessionartifacts.VersionsDir {
			return filepath.SkipDir
		}
		if entry.Type().IsRegular() {
			paths[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("list artifacts of %s: %w", source, err)
	}
	if lister != nil {
		latest, err := lister.ListLatestSessionArtifactVersions(ctx, source)
		if err != nil {
			return fmt.Errorf("list saved artifact versions of %s: %w", source, err)
		}
		for _, version := range latest {
			paths[version.Path] = true
		}
	}
	for rel := range paths {
		if err := m.copyArtifactAsOf(ctx, lister, source, src, dst, rel, asOf); err != nil {
			return fmt.Errorf("copy artifact %s of %s: %w", rel, source, err)
		}
	}
	return nil
}

func copyTurnArtifacts(ctx context.Context, src, dst string, files map[string]string) error {
	for rel, sum := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		local := filepath.FromSlash(rel)
		if !filepath.IsLocal(local) {
			continue
		}
		if err := copyRegularFileMkdir(sessionartifacts.KeptBlobPath(src, sum), filepath.Join(dst, local)); err != nil {
			return fmt.Errorf("copy artifact %s: %w", rel, err)
		}
	}
	return nil
}

func (m *Manager) copyArtifactAsOf(ctx context.Context, lister artifactVersionLister, source domain.SessionID, src, dst, rel string, asOf time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	local := filepath.FromSlash(rel)
	if !filepath.IsLocal(local) {
		return nil
	}
	target := filepath.Join(dst, local)
	var versions []domain.SessionArtifactVersion
	if lister != nil {
		var err error
		if versions, err = lister.ListSessionArtifactVersions(ctx, source, rel); err != nil {
			return err
		}
	}
	if len(versions) == 0 {
		info, err := os.Lstat(filepath.Join(src, local))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(asOf) {
			return err
		}
		return copyRegularFileMkdir(filepath.Join(src, local), target)
	}
	var chosen *domain.SessionArtifactVersion
	for i := range versions {
		if !versions[i].ModTime.After(asOf) && (chosen == nil || versions[i].Version > chosen.Version) {
			chosen = &versions[i]
		}
	}
	if chosen == nil {
		return nil
	}
	blob := filepath.Join(src, sessionartifacts.VersionsDir, "blobs", chosen.SHA256[:2], chosen.SHA256)
	return copyRegularFileMkdir(blob, target)
}

func copyRegularFileMkdir(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return copyRegularFile(from, to)
}

func (m *Manager) copyForkAttachments(ctx context.Context, fork *ports.SpawnFork, id domain.SessionID) error {
	if fork.SourceSessionID == "" || m.dataDir == "" {
		return nil
	}
	var only map[string]bool
	if fork.LimitAttachments {
		only = map[string]bool{}
		for _, name := range fork.AttachmentNames {
			only[name] = true
		}
	}
	root := filepath.Join(m.dataDir, "attachments")
	if err := copyTree(ctx, filepath.Join(root, string(fork.SourceSessionID)), filepath.Join(root, string(id)), "", only); err != nil {
		return fmt.Errorf("copy attachments of %s: %w", fork.SourceSessionID, err)
	}
	return nil
}

func copyTree(ctx context.Context, src, dst, skip string, only map[string]bool) error {
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if skip != "" && entry.IsDir() && rel == skip {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		switch {
		case entry.IsDir():
			if only != nil && rel != "." {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o700)
		case entry.Type().IsRegular():
			if only != nil && !only[rel] {
				return nil
			}
			return copyRegularFile(path, target)
		default:
			return nil
		}
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

type forkLinkRewriter interface {
	ConversationForSession(ctx context.Context, id domain.SessionID) (domain.ConversationRecord, error)
	RewriteConversationText(ctx context.Context, conversationID, from, to string) (int, error)
	CopyForkMessageContent(ctx context.Context, sourceConversationID, forkConversationID string) (int, error)
}

func (m *Manager) pointForkLinksAtFork(ctx context.Context, project domain.ProjectID, source, fork domain.SessionID) {
	rewriter, ok := m.store.(forkLinkRewriter)
	if !ok || source == "" {
		return
	}
	conversation, err := rewriter.ConversationForSession(ctx, fork)
	if err != nil {
		m.logger.Warn("session fork: find conversation for link rewrite", "session", fork, "err", err)
		return
	}
	from := "ao://sessions/" + string(project) + "/" + string(source) + "/artifacts/"
	to := "ao://sessions/" + string(project) + "/" + string(fork) + "/artifacts/"
	if _, err := rewriter.RewriteConversationText(ctx, conversation.ID, from, to); err != nil {
		m.logger.Warn("session fork: point artifact links at the fork", "session", fork, "err", err)
	}
	if original, err := rewriter.ConversationForSession(ctx, source); err != nil {
		m.logger.Warn("session fork: find the original's conversation", "session", source, "err", err)
	} else if _, err := rewriter.CopyForkMessageContent(ctx, original.ID, conversation.ID); err != nil {
		m.logger.Warn("session fork: copy images from the original", "session", fork, "err", err)
	}
}

func copyRegularFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

type forkOriginKey struct{}

type forkOrigin struct {
	sourceID  domain.SessionID
	title     string
	workspace string
}

func withForkOrigin(ctx context.Context, sourceID domain.SessionID, title, workspace string) context.Context {
	return context.WithValue(ctx, forkOriginKey{}, forkOrigin{sourceID: sourceID, title: title, workspace: workspace})
}

type forkLineageReader interface {
	GetSessionFork(ctx context.Context, forkID domain.SessionID) (domain.SessionFork, bool, error)
}

func (m *Manager) forkBoundaryPrompt(ctx context.Context, id domain.SessionID) string {
	origin, ok := ctx.Value(forkOriginKey{}).(forkOrigin)
	if !ok {
		lineage, isReader := m.store.(forkLineageReader)
		if !isReader {
			return ""
		}
		fork, found, err := lineage.GetSessionFork(ctx, id)
		if err != nil || !found {
			return ""
		}
		origin = forkOrigin{sourceID: fork.SourceSessionID, title: fork.SourceTitle}
		if source, found, err := m.store.GetSession(ctx, fork.SourceSessionID); err == nil && found {
			origin.workspace = source.Metadata.WorkspacePath
		}
	}
	if origin.sourceID == "" {
		return ""
	}
	title := ""
	if origin.title != "" {
		title = fmt.Sprintf(" (%q)", origin.title)
	}
	worktree := ""
	if path := strings.TrimSpace(origin.workspace); path != "" {
		worktree = fmt.Sprintf(" The original's worktree is `%s`, and the conversation you copied names paths in it. Those files are the original's: never read, write, or run commands in that folder. The same files are in this session's own worktree, your working directory; use the same relative paths there.", path)
	}
	return fmt.Sprintf(`## Session Fork

This session is a fork of Orchestrator session %s%s. It started with that session's conversation and files, but it is a separate session (%s) with its own worktree, branch, and artifact folder. Work only in this session's own worktree; never edit the original's worktree.%s

The original's pull requests belong to the original session. Don't push to their branches, comment on them, request reviews of them, or claim them. If this work needs a PR, open a new one from this session's branch; target the original's branch when this work builds on it.

Plans and tasks from before the fork are background. Continue only what the user asks in this session, and report progress as this session, not as the original.`, origin.sourceID, title, id, worktree)
}

func (m *Manager) isSessionFork(ctx context.Context, id domain.SessionID) bool {
	lineage, ok := m.store.(forkLineageReader)
	if !ok {
		return false
	}
	_, found, err := lineage.GetSessionFork(ctx, id)
	return err == nil && found
}

func (m *Manager) cleanupForkLeftovers(rec domain.SessionRecord, nativeFile string) {
	m.cleanupArtifactDir(rec.ID)
	if project, err := m.loadProject(context.Background(), rec.ProjectID); err == nil && project.Path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = m.forkSeeds.Delete(ctx, project.Path, forkseed.BaseRef(string(rec.ID)))
		cancel()
	}
	if nativeFile == "" {
		return
	}
	if err := os.Remove(nativeFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logger.Warn("session fork: remove the agent's session copy", "session", rec.ID, "path", nativeFile, "err", err)
	}
}

func (m *Manager) forkNativeSessionFile(ctx context.Context, rec domain.SessionRecord) string {
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return ""
	}
	configs, hasConfig := agent.(ports.AgentNativeSessionConfigProvider)
	locator, hasLocator := agent.(ports.AgentTranscriptLocator)
	if !hasConfig || !hasLocator {
		return ""
	}
	native := rec.Metadata.ProviderConversationID
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		native = rec.Metadata.AgentSessionID
	}
	if strings.TrimSpace(native) == "" {
		return ""
	}
	var env map[string]string
	if project, err := m.loadProject(ctx, rec.ProjectID); err == nil {
		env = m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	}
	dir, err := configs.NativeSessionConfigDir(ctx, env)
	if err != nil || dir == "" {
		return ""
	}
	path, found, err := locator.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: native, ConfigDir: dir})
	if err != nil || !found {
		return ""
	}
	return path
}

func (m *Manager) discardForkConversation(ctx context.Context, harness domain.AgentHarness, providerID, workspace string, env map[string]string) {
	if harness != domain.HarnessClaudeCode || providerID == "" || workspace == "" || m.claudeSessionCleanup == nil {
		return
	}
	if err := m.claudeSessionCleanup(context.WithoutCancel(ctx), providerID, workspace, env); err != nil {
		m.logger.Warn("session fork: remove Claude session copy", "session", providerID, "err", err)
	}
}

type ForkRetrier func(ctx context.Context, fork domain.SessionRecord) (*ports.SpawnFork, error)

func (m *Manager) SetForkRetrier(retry ForkRetrier) { m.forkRetry = retry }

func (m *Manager) dropInheritedForkChanges(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo) bool {
	lineage, ok := m.store.(forkLineageReader)
	if !ok || ws.Path == "" {
		return false
	}
	fork, found, err := lineage.GetSessionFork(ctx, rec.ID)
	if err != nil || !found {
		return false
	}
	source, found, err := m.store.GetSession(ctx, fork.SourceSessionID)
	if err != nil || !found || source.IsTerminated {
		return false
	}
	unchanged, err := m.forkSeeds.UnchangedSince(ctx, ws.Path, forkseed.BaseRef(string(rec.ID)))
	if err != nil || !unchanged {
		return false
	}
	if err := m.forkSeeds.DiscardChanges(ctx, ws.Path); err != nil {
		m.logger.Warn("session fork: drop inherited changes before cleanup", "session", rec.ID, "err", err)
		return false
	}
	return true
}

func (m *Manager) restoreForkStartingFiles(ctx context.Context, id domain.SessionID, worktree string) {
	if worktree == "" || !m.isSessionFork(ctx, id) {
		return
	}
	if _, err := m.forkSeeds.RestoreBase(ctx, worktree, forkseed.BaseRef(string(id))); err != nil {
		m.logger.Warn("session fork: put starting files back after restore", "session", id, "err", err)
	}
}
