package sessionmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/nativetransfer"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

var (
	// ErrTransferUnsupported refuses a session this build cannot move.
	ErrTransferUnsupported = errors.New("session: this session cannot be moved")
	// ErrTransferAgentBusy refuses to move a session whose agent is mid-turn.
	ErrTransferAgentBusy = errors.New("the agent is in the middle of a turn; move the session when it finishes")
	// ErrTransferRepoMismatch refuses a project of a different repository.
	ErrTransferRepoMismatch = errors.New("session: the project here is a different repository")
	// ErrTransferPreservedMismatch refuses uncommitted work captured on another head.
	ErrTransferPreservedMismatch = errors.New("session: the moved uncommitted work does not belong to the moved head")
	// ErrTransferArchive reports a missing or damaged conversation archive.
	ErrTransferArchive = errors.New("session: the moved conversation archive is missing or damaged")
	// ErrSessionMoved refuses to run a session that continues on another machine.
	ErrSessionMoved = errors.New("session: moved to another computer")
	// ErrSpawnTransfer is the spawn stage that applies moved state.
	ErrSpawnTransfer = errors.New("transfer")
)

// MaxTransferArchiveBytes caps an uploaded conversation archive.
const MaxTransferArchiveBytes int64 = 4 << 30

var transferIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)

type sessionTransferStore interface {
	RecordSessionTransfer(ctx context.Context, id domain.SessionID, moved domain.SessionMovedTo, at time.Time) error
	SessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error)
}

func (m *Manager) transferDir(transferID string) (string, error) {
	if !transferIDPattern.MatchString(transferID) {
		return "", fmt.Errorf("%w: invalid transfer id", ErrTransferUnsupported)
	}
	return filepath.Join(m.dataDir, "transfers", transferID), nil
}

// PrepareTransfer stops the session's agent and hands over its state: it
// pushes the branch and any uncommitted work to origin and exports the agent's
// conversation into the data dir. The session itself is left as it was, so
// resume-agent undoes everything. Calling it again with the same transfer id
// returns the first result.
func (m *Manager) PrepareTransfer(ctx context.Context, id domain.SessionID, transferID string) (domain.SessionTransfer, error) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	payloadPath := filepath.Join(dir, "payload.json")
	if data, err := os.ReadFile(payloadPath); err == nil {
		var t domain.SessionTransfer
		if err := json.Unmarshal(data, &t); err != nil {
			return domain.SessionTransfer{}, err
		}
		if t.Source.SessionID != id {
			return domain.SessionTransfer{}, fmt.Errorf("%w: transfer %s belongs to another session", ErrTransferUnsupported, transferID)
		}
		return t, nil
	}
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	if err := transferable(rec, project); err != nil {
		return domain.SessionTransfer{}, err
	}
	if _, moved, err := m.sessionMovedTo(ctx, id); err != nil {
		return domain.SessionTransfer{}, err
	} else if moved {
		return domain.SessionTransfer{}, ErrSessionMoved
	}
	git, ok := m.workspace.(ports.WorkspaceTransfer)
	if !ok {
		return domain.SessionTransfer{}, fmt.Errorf("%w: workspace adapter cannot transfer", ErrTransferUnsupported)
	}
	if rec.Activity.State == domain.ActivityActive {
		return domain.SessionTransfer{}, ErrTransferAgentBusy
	}
	if _, err := m.ExitAgent(ctx, id); err != nil && !errors.Is(err, ErrAgentExited) {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: stop agent: %w", id, err)
	}

	ws := workspaceInfo(rec)
	localRef, err := m.workspace.StashUncommitted(ctx, ws)
	if err != nil {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: capture uncommitted work: %w", id, err)
	}
	remoteRef := ""
	if localRef != "" {
		remoteRef = "refs/ao/preserved/" + transferID
	}
	head, err := git.PushTransfer(ctx, ws, localRef, remoteRef)
	if err != nil {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: push: %w", id, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return domain.SessionTransfer{}, err
	}
	native, err := m.exportTransferConversation(ctx, rec, project, filepath.Join(dir, "native.tgz"))
	if err != nil {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: export conversation: %w", id, err)
	}
	t := domain.SessionTransfer{
		TransferID:      transferID,
		Source:          domain.SessionTransferSource{SessionID: id},
		RepoKey:         domain.RepositoryKey(project.RepoOriginURL),
		Branch:          rec.Metadata.Branch,
		HeadSHA:         head,
		PreservedRef:    remoteRef,
		Native:          native,
		Harness:         rec.Harness,
		Model:           rec.Metadata.Model,
		Effort:          rec.Metadata.Effort,
		Permissions:     rec.Metadata.Permissions,
		DisplayName:     rec.DisplayName,
		IssueID:         rec.IssueID,
		RequestedOutput: rec.RequestedOutput,
	}
	if err := writeFileAtomic(payloadPath, t); err != nil {
		return domain.SessionTransfer{}, err
	}
	return t, nil
}

func transferable(rec domain.SessionRecord, project domain.ProjectRecord) error {
	switch {
	case rec.IsTerminated:
		return fmt.Errorf("%w: it has ended", ErrTransferUnsupported)
	case rec.Kind != domain.KindWorker:
		return fmt.Errorf("%w: only workers can move", ErrTransferUnsupported)
	case rec.Harness != domain.HarnessClaudeCode:
		return fmt.Errorf("%w: moving %s sessions is not supported yet", ErrTransferUnsupported, rec.Harness)
	case rec.ProjectID == "" || project.Kind.WithDefault() != domain.ProjectKindSingleRepo:
		return fmt.Errorf("%w: only single-repository projects can move", ErrTransferUnsupported)
	case rec.Metadata.Branch == "" || rec.Metadata.WorkspacePath == "":
		return fmt.Errorf("%w: the session has no worktree branch", ErrTransferUnsupported)
	}
	return nil
}

// exportTransferConversation writes the agent's conversation archive and
// describes it. A session whose agent never wrote a conversation exports none.
func (m *Manager) exportTransferConversation(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, archive string) (*domain.SessionTransferNative, error) {
	configDir, err := m.nativeConfigDir(ctx, rec.Harness, m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env))
	if err != nil {
		return nil, err
	}
	nativeID := rec.Metadata.AgentSessionID
	if nativeID == "" {
		nativeID = rec.Metadata.ProviderConversationID
	}
	if nativeID == "" {
		nativeID = agentruntime.ClaudeSessionID(string(rec.ID))
	}
	tmp := archive + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	manifest, exportErr := nativetransfer.ExportClaude(ctx, f, nativetransfer.ClaudeExportOptions{ConfigDir: configDir, NativeID: nativeID, Cwd: rec.Metadata.WorkspacePath})
	if err := f.Close(); exportErr == nil {
		exportErr = err
	}
	if exportErr != nil {
		_ = os.Remove(tmp)
		m.logger.Warn("transfer: no conversation to move; the destination starts from the handoff note", "sessionID", rec.ID, "error", exportErr)
		return nil, nil
	}
	if err := os.Rename(tmp, archive); err != nil {
		return nil, err
	}
	size, sum, err := fileDigest(archive)
	if err != nil {
		return nil, err
	}
	return &domain.SessionTransferNative{NativeID: manifest.NativeID, SourceCwd: manifest.SourceCwd, CLIVersion: manifest.CLIVersion, Size: size, SHA256: sum}, nil
}

func (m *Manager) nativeConfigDir(ctx context.Context, harness domain.AgentHarness, env map[string]string) (string, error) {
	agent, ok := m.agents.Agent(harness)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownHarness, harness)
	}
	provider, ok := agent.(ports.AgentNativeSessionConfigProvider)
	if !ok {
		return "", fmt.Errorf("%w: %s keeps no native conversation AO can move", ErrTransferUnsupported, harness)
	}
	return provider.NativeSessionConfigDir(ctx, env)
}

// TransferArchivePath is the exported conversation of a prepared transfer.
func (m *Manager) TransferArchivePath(transferID string) (string, error) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "native.tgz")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("%w: %w", ErrTransferArchive, err)
	}
	return path, nil
}

// StoreTransferArchivePart keeps one uploaded part of a transfer's archive.
// Parts are numbered from 0 and may arrive more than once.
func (m *Manager) StoreTransferArchivePart(transferID string, part int, r io.Reader) error {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return err
	}
	if part < 0 || part > 99999 {
		return fmt.Errorf("%w: invalid part %d", ErrTransferArchive, part)
	}
	parts := filepath.Join(dir, "incoming")
	if err := os.MkdirAll(parts, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parts, ".part-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, io.LimitReader(r, MaxTransferArchiveBytes))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(parts, fmt.Sprintf("part-%05d", part)))
}

// assembleTransferArchive joins the uploaded parts in order and checks the
// result against the transfer's size and checksum.
func (m *Manager) assembleTransferArchive(transferID string, native domain.SessionTransferNative) (string, error) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return "", err
	}
	archive := filepath.Join(dir, "received.tgz")
	if size, sum, err := fileDigest(archive); err == nil && size == native.Size && sum == native.SHA256 {
		return archive, nil
	}
	out, err := os.OpenFile(archive+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var total int64
	for part := 0; total < native.Size; part++ {
		f, err := os.Open(filepath.Join(dir, "incoming", fmt.Sprintf("part-%05d", part)))
		if err != nil {
			_ = out.Close()
			return "", fmt.Errorf("%w: part %d: %w", ErrTransferArchive, part, err)
		}
		n, err := io.Copy(io.MultiWriter(out, h), f)
		_ = f.Close()
		if err != nil {
			_ = out.Close()
			return "", err
		}
		if n == 0 {
			_ = out.Close()
			return "", fmt.Errorf("%w: part %d is empty", ErrTransferArchive, part)
		}
		total += n
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if total != native.Size || hex.EncodeToString(h.Sum(nil)) != native.SHA256 {
		_ = os.Remove(archive + ".tmp")
		return "", fmt.Errorf("%w: size or checksum does not match", ErrTransferArchive)
	}
	return archive, os.Rename(archive+".tmp", archive)
}

// ReceiveTransfer starts a session here that continues a transfer prepared
// on another machine. prompt is the first turn of the resumed conversation.
// A retry with the same transfer id returns the session already started.
func (m *Manager) ReceiveTransfer(ctx context.Context, projectID domain.ProjectID, t domain.SessionTransfer, prompt string) (domain.SessionRecord, error) {
	if _, err := m.transferDir(t.TransferID); err != nil {
		return domain.SessionRecord{}, err
	}
	clientRequestID := "transfer-" + t.TransferID
	if existing, found, err := m.store.GetSessionByClientRequestID(ctx, clientRequestID); err != nil {
		return domain.SessionRecord{}, err
	} else if found {
		return existing, nil
	}
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	if project.Kind.WithDefault() != domain.ProjectKindSingleRepo {
		return domain.SessionRecord{}, fmt.Errorf("%w: only single-repository projects can receive a session", ErrTransferUnsupported)
	}
	if key := domain.RepositoryKey(project.RepoOriginURL); key == "" || key != t.RepoKey {
		return domain.SessionRecord{}, ErrTransferRepoMismatch
	}
	archive := ""
	if t.Native != nil {
		if archive, err = m.assembleTransferArchive(t.TransferID, *t.Native); err != nil {
			return domain.SessionRecord{}, err
		}
	}
	git, ok := m.workspace.(ports.WorkspaceTransfer)
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("%w: workspace adapter cannot transfer", ErrTransferUnsupported)
	}
	if err := git.FetchTransfer(ctx, project.Path, t.Branch, t.PreservedRef); err != nil {
		return domain.SessionRecord{}, err
	}
	if err := git.AlignTransferBranch(ctx, project.Path, t.Branch, t.HeadSHA); err != nil {
		return domain.SessionRecord{}, err
	}
	if t.PreservedRef != "" {
		parent, err := git.RevParse(ctx, project.Path, t.PreservedRef+"^")
		if err != nil || parent != t.HeadSHA {
			return domain.SessionRecord{}, ErrTransferPreservedMismatch
		}
	}
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID:         projectID,
		IssueID:           t.IssueID,
		Kind:              domain.KindWorker,
		Harness:           t.Harness,
		Branch:            t.Branch,
		RequestedMode:     domain.SessionModeTUI,
		Prompt:            prompt,
		DisplayName:       t.DisplayName,
		KeepDisplayName:   t.DisplayName != "",
		RequestedOutput:   t.RequestedOutput,
		AgentConfig:       ports.AgentConfig{Model: t.Model, Effort: t.Effort, Permissions: t.Permissions},
		EffortOverride:    t.Effort != "",
		ClientRequestID:   clientRequestID,
		ClientRequestHash: transferRequestHash(t),
		Transfer: &ports.SpawnTransfer{
			PreservedRef:   t.PreservedRef,
			NativeArchive:  archive,
			FallbackPrompt: t.HandoffNote,
		},
	})
	return rec, err
}

// placeTransferConversation puts a moved conversation where the agent CLI
// finds it from workspacePath and returns its native id.
func (m *Manager) placeTransferConversation(ctx context.Context, harness domain.AgentHarness, workspacePath string, env map[string]string, archive string) (string, error) {
	if harness != domain.HarnessClaudeCode {
		return "", fmt.Errorf("%w: %s", ErrTransferUnsupported, harness)
	}
	configDir, err := m.nativeConfigDir(ctx, harness, env)
	if err != nil {
		return "", err
	}
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	res, err := nativetransfer.ImportClaude(ctx, f, nativetransfer.ClaudeImportOptions{ConfigDir: configDir, LaunchCwd: workspacePath})
	if err != nil {
		return "", err
	}
	return res.NativeID, nil
}

// transferRestoreArgv builds the command that resumes a placed conversation
// and submits launchCfg.Prompt as its next turn.
func transferRestoreArgv(ctx context.Context, agent ports.Agent, nativeID string, launchCfg ports.LaunchConfig) ([]string, error) {
	cmd, ok, err := agent.GetRestoreCommand(ctx, ports.RestoreConfig{
		Session: ports.SessionRef{
			ID:            launchCfg.SessionID,
			WorkspacePath: launchCfg.WorkspacePath,
			Metadata: map[string]string{
				ports.MetadataKeyAgentSessionID: nativeID,
				ports.MetadataKeySessionMode:    string(domain.SessionModeTUI),
			},
		},
		Kind:             launchCfg.Kind,
		DataDir:          launchCfg.DataDir,
		Config:           launchCfg.Config,
		Permissions:      launchCfg.Permissions,
		Prompt:           launchCfg.Prompt,
		SystemPrompt:     launchCfg.SystemPrompt,
		SystemPromptFile: launchCfg.SystemPromptFile,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: the agent cannot resume conversation %s", ErrTransferUnsupported, nativeID)
	}
	return cmd, nil
}

// CommitTransfer marks a prepared session as moved and ends it here. Its
// worktree is kept when it has uncommitted work. A live orchestrator of the
// project is told, so it does not start the work again. Committing twice keeps
// the first destination.
func (m *Manager) CommitTransfer(ctx context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination) (domain.SessionMovedTo, error) {
	store, ok := m.store.(sessionTransferStore)
	if !ok {
		return domain.SessionMovedTo{}, fmt.Errorf("%w: store cannot record transfers", ErrTransferUnsupported)
	}
	if _, err := m.transferDir(transferID); err != nil {
		return domain.SessionMovedTo{}, err
	}
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionMovedTo{}, err
	}
	if err := store.RecordSessionTransfer(ctx, id, domain.SessionMovedTo{TransferID: transferID, HostID: dest.HostID, Label: dest.Label, SessionID: dest.SessionID}, m.clock()); err != nil {
		return domain.SessionMovedTo{}, err
	}
	moved, _, err := store.SessionMovedTo(ctx, id)
	if err != nil {
		return domain.SessionMovedTo{}, err
	}
	if !rec.IsTerminated {
		if _, err := m.Kill(ctx, id); err != nil && !errors.Is(err, ErrTerminated) {
			return moved, fmt.Errorf("commit transfer %s: end session: %w", id, err)
		}
		if orchestrator, ok, err := m.activeOrchestratorSessionID(ctx, rec.ProjectID); err == nil && ok && orchestrator != id {
			note := fmt.Sprintf("Worker %s moved to %s and continues there as session %s. It cannot be messaged from here. Do not respawn its work.", id, moved.Label, moved.SessionID)
			if err := m.Send(ctx, orchestrator, note, nil); err != nil {
				m.logger.Warn("transfer: could not tell the orchestrator", "sessionID", id, "orchestrator", orchestrator, "error", err)
			}
		}
	}
	// Free the branch so the session can later move back to this computer.
	if git, ok := m.workspace.(ports.WorkspaceTransfer); ok && dirExists(rec.Metadata.WorkspacePath) {
		if err := git.ReleaseTransferBranch(ctx, workspaceInfo(rec)); err != nil {
			m.logger.Warn("transfer: could not release the branch", "sessionID", id, "error", err)
		}
	}
	return moved, nil
}

// SessionMovedTo reports where a moved session continues.
func (m *Manager) SessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error) {
	return m.sessionMovedTo(ctx, id)
}

func (m *Manager) sessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error) {
	store, ok := m.store.(sessionTransferStore)
	if !ok {
		return domain.SessionMovedTo{}, false, nil
	}
	return store.SessionMovedTo(ctx, id)
}

// refuseMoved keeps a session that continues elsewhere from running here again.
func (m *Manager) refuseMoved(ctx context.Context, id domain.SessionID) error {
	moved, ok, err := m.sessionMovedTo(ctx, id)
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("%w: continues on %s as %s", ErrSessionMoved, moved.Label, moved.SessionID)
	}
	return nil
}

// transferRequestHash binds a receive retry to the same transfer content.
func transferRequestHash(t domain.SessionTransfer) string {
	sum := sha256.Sum256([]byte(t.TransferID + "\x00" + t.Branch + "\x00" + t.HeadSHA + "\x00" + t.PreservedRef))
	return hex.EncodeToString(sum[:])
}

func fileDigest(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), err
}

func writeFileAtomic(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// dirExists reports whether path is a directory; ending a session removes a clean worktree.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return path != "" && err == nil && info.IsDir()
}
