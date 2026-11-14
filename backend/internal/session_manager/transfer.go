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
	"strings"
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
	// ErrTransferPending refuses to run a session whose move did not finish.
	ErrTransferPending = errors.New("a move of this session to another computer did not finish")
	// ErrTransferDefaultBranch refuses a session working on the default branch.
	ErrTransferDefaultBranch = errors.New("the session works on the project's default branch; moving it would push that branch to origin")
)

// MaxTransferArchiveBytes caps an uploaded conversation archive.
const MaxTransferArchiveBytes int64 = 4 << 30

// transferStaleAfter is when an unfinished transfer's files are swept.
const transferStaleAfter = 7 * 24 * time.Hour

var (
	transferIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)
	commitSHAPattern  = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

type sessionTransferStore interface {
	RecordPendingTransfer(ctx context.Context, id domain.SessionID, dest domain.SessionMovedTo, at time.Time) error
	CancelPendingTransfer(ctx context.Context, id domain.SessionID, transferID string) error
	RecordSessionTransfer(ctx context.Context, id domain.SessionID, moved domain.SessionMovedTo, at time.Time) error
	SessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error)
}

func (m *Manager) transferDir(transferID string) (string, error) {
	if !transferIDPattern.MatchString(transferID) {
		return "", fmt.Errorf("%w: invalid transfer id", ErrTransferUnsupported)
	}
	return filepath.Join(m.dataDir, "transfers", transferID), nil
}

// readTransferPayload returns a prepared transfer of session id.
func (m *Manager) readTransferPayload(id domain.SessionID, transferID string) (domain.SessionTransfer, bool, error) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return domain.SessionTransfer{}, false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "payload.json"))
	if errors.Is(err, os.ErrNotExist) {
		return domain.SessionTransfer{}, false, nil
	}
	if err != nil {
		return domain.SessionTransfer{}, false, err
	}
	var t domain.SessionTransfer
	if err := json.Unmarshal(data, &t); err != nil {
		return domain.SessionTransfer{}, false, err
	}
	if t.Source.SessionID != id {
		return domain.SessionTransfer{}, false, fmt.Errorf("%w: transfer %s belongs to another session", ErrTransferUnsupported, transferID)
	}
	return t, true, nil
}

// PrepareTransfer stops the session's agent and hands over its state: it
// pushes the branch and any uncommitted work to origin and exports the agent's
// conversation into the data dir. It then marks the session as being moved to
// dest, so it cannot run here until CommitTransfer or CancelTransfer. Calling
// it again with the same transfer id, or for an unfinished move to the same
// host, returns the earlier result. A mid-turn agent is refused unless
// stopTurn is set, which ends the turn.
func (m *Manager) PrepareTransfer(ctx context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination, stopTurn bool) (domain.SessionTransfer, error) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	if t, ok, err := m.readTransferPayload(id, transferID); err != nil || ok {
		return t, err
	}
	if state, ok, err := m.transferState(ctx, id); err != nil {
		return domain.SessionTransfer{}, err
	} else if ok && state.Pending() {
		if state.HostID != dest.HostID {
			return domain.SessionTransfer{}, fmt.Errorf("%w: finish the move to %s first", ErrTransferPending, state.Label)
		}
		if t, ok, err := m.readTransferPayload(id, state.TransferID); err != nil || ok {
			return t, err
		}
	}
	m.sweepTransfers(ctx)
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
	if refresher, ok := m.workspace.(ports.WorkspaceDefaultBranchRefresher); ok {
		def, err := refresher.ResolveDefaultBranch(ctx, project.Path, project.Config.WorktreeBaseBranch())
		if err == nil && def.Branch != "" && def.Branch == rec.Metadata.Branch {
			return domain.SessionTransfer{}, ErrTransferDefaultBranch
		}
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
	if !stopTurn && (rec.Activity.State == domain.ActivityActive || rec.Activity.State == domain.ActivityBlocked) {
		return domain.SessionTransfer{}, ErrTransferAgentBusy
	}
	if _, err := m.ExitAgent(ctx, id); err != nil && !errors.Is(err, ErrAgentExited) {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: stop agent: %w", id, err)
	}

	var head, remoteRef string
	err = func() error {
		// A new session's branch is renamed under this gate once its title is
		// generated; read the branch and push it without a rename in between.
		defer m.acquireWorkspaceGate(rec.ProjectID)()
		if rec, err = m.getRecord(ctx, id); err != nil {
			return err
		}
		ws := workspaceInfo(rec)
		localRef, err := m.workspace.StashUncommitted(ctx, ws)
		if err != nil {
			return fmt.Errorf("transfer %s: capture uncommitted work: %w", id, err)
		}
		if localRef != "" {
			remoteRef = "refs/ao/preserved/" + transferID
		}
		if head, err = git.PushTransfer(ctx, ws, localRef, remoteRef); err != nil {
			return fmt.Errorf("transfer %s: push: %w", id, err)
		}
		return nil
	}()
	if err != nil {
		return domain.SessionTransfer{}, err
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
	if err := writeFileAtomic(filepath.Join(dir, "payload.json"), t); err != nil {
		return domain.SessionTransfer{}, err
	}
	if store, ok := m.store.(sessionTransferStore); ok {
		pending := domain.SessionMovedTo{TransferID: transferID, HostID: dest.HostID, Label: dest.Label}
		if err := store.RecordPendingTransfer(ctx, id, pending, m.clock()); err != nil {
			return domain.SessionTransfer{}, err
		}
	}
	return t, nil
}

// CancelTransfer forgets an unfinished move so the session can run here again,
// and removes what the move left behind. The agent stays stopped.
func (m *Manager) CancelTransfer(ctx context.Context, id domain.SessionID, transferID string) error {
	if _, err := m.transferDir(transferID); err != nil {
		return err
	}
	if store, ok := m.store.(sessionTransferStore); ok {
		if err := store.CancelPendingTransfer(ctx, id, transferID); err != nil {
			return err
		}
	}
	m.removeSourceTransfer(ctx, id, transferID)
	return nil
}

// removeSourceTransfer deletes a prepared transfer's files and its preserved
// ref on origin. Failures are logged: the move itself is already settled.
func (m *Manager) removeSourceTransfer(ctx context.Context, id domain.SessionID, transferID string) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return
	}
	if t, ok, err := m.readTransferPayload(id, transferID); err == nil && ok && t.PreservedRef != "" {
		m.deletePreservedRef(ctx, id, t.PreservedRef)
	}
	if err := os.RemoveAll(dir); err != nil {
		m.logger.Warn("transfer: could not remove transfer files", "transferID", transferID, "error", err)
	}
}

func (m *Manager) deletePreservedRef(ctx context.Context, id domain.SessionID, ref string) {
	git, ok := m.workspace.(ports.WorkspaceTransfer)
	if !ok {
		return
	}
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return
	}
	if err := git.DeleteTransferRef(ctx, project.Path, ref); err != nil {
		m.logger.Warn("transfer: could not delete the preserved ref from origin", "sessionID", id, "ref", ref, "error", err)
	}
}

// sweepTransfers removes transfer files, and their preserved refs on origin,
// left by moves that never finished.
// ponytail: runs on the next prepare or receive; add a daemon-start sweep if they pile up.
func (m *Manager) sweepTransfers(ctx context.Context) {
	root := filepath.Join(m.dataDir, "transfers")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(root, e.Name(), "payload.json"))
		// Uploads for a receive are redone on every attempt, so they go stale sooner.
		staleAfter := transferStaleAfter
		if errors.Is(readErr, os.ErrNotExist) {
			staleAfter = time.Hour
		}
		if m.clock().Sub(info.ModTime()) < staleAfter {
			continue
		}
		var t domain.SessionTransfer
		if readErr == nil && json.Unmarshal(data, &t) == nil && t.PreservedRef != "" {
			m.deletePreservedRef(ctx, t.Source.SessionID, t.PreservedRef)
		}
		_ = os.RemoveAll(filepath.Join(root, e.Name()))
	}
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

// TransferArchivePath is the exported conversation of session id's prepared transfer.
func (m *Manager) TransferArchivePath(id domain.SessionID, transferID string) (string, error) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return "", err
	}
	if _, ok, err := m.readTransferPayload(id, transferID); err != nil || !ok {
		return "", fmt.Errorf("%w: no prepared transfer %s for session %s", ErrTransferArchive, transferID, id)
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
	name := fmt.Sprintf("part-%05d", part)
	var used int64
	if entries, err := os.ReadDir(parts); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && e.Name() != name {
				used += info.Size()
			}
		}
	}
	tmp, err := os.CreateTemp(parts, ".part-*")
	if err != nil {
		return err
	}
	n, err := io.Copy(tmp, io.LimitReader(r, MaxTransferArchiveBytes-used+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil && used+n > MaxTransferArchiveBytes {
		err = fmt.Errorf("%w: larger than %d bytes", ErrTransferArchive, MaxTransferArchiveBytes)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(parts, name))
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
	if _, err := os.Stat(filepath.Join(dir, "incoming")); err != nil {
		return "", fmt.Errorf("%w: the conversation was not uploaded", ErrTransferArchive)
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
func (m *Manager) ReceiveTransfer(ctx context.Context, projectID domain.ProjectID, t domain.SessionTransfer, prompt string) (rec domain.SessionRecord, err error) {
	dir, err := m.transferDir(t.TransferID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	if !commitSHAPattern.MatchString(t.HeadSHA) {
		return domain.SessionRecord{}, fmt.Errorf("%w: invalid head %q", ErrTransferUnsupported, t.HeadSHA)
	}
	if ref, ok := strings.CutPrefix(t.PreservedRef, "refs/ao/preserved/"); t.PreservedRef != "" && (!ok || !transferIDPattern.MatchString(ref)) {
		return domain.SessionRecord{}, fmt.Errorf("%w: invalid preserved ref %q", ErrTransferUnsupported, t.PreservedRef)
	}
	clientRequestID := "transfer-" + t.TransferID
	if existing, found, err := m.store.GetSessionByClientRequestID(ctx, clientRequestID); err != nil {
		return domain.SessionRecord{}, err
	} else if found {
		_ = os.RemoveAll(dir)
		return existing, nil
	}
	m.sweepTransfers(ctx)
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
	rec, _, _, err = m.Spawn(ctx, ports.SpawnConfig{
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
	if err == nil {
		_ = os.RemoveAll(dir)
		m.tellOrchestratorAboutArrival(ctx, rec, t)
	}
	return rec, err
}

// ProjectOrchestrator is the project's live orchestrator session, if it has one.
func (m *Manager) ProjectOrchestrator(ctx context.Context, project domain.ProjectID) (domain.SessionID, bool, error) {
	return m.activeOrchestratorSessionID(ctx, project)
}

// tellOrchestratorAboutArrival lets the project's live orchestrator know that a
// worker it did not spawn now runs here. Delivery waits for the orchestrator,
// so it runs after the receive has answered.
func (m *Manager) tellOrchestratorAboutArrival(ctx context.Context, rec domain.SessionRecord, t domain.SessionTransfer) {
	orchestrator, ok, err := m.activeOrchestratorSessionID(ctx, rec.ProjectID)
	if err != nil || !ok || orchestrator == rec.ID {
		return
	}
	note := transferArrivalNote(rec.ID, t)
	m.runInBackground(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if err := m.Send(ctx, orchestrator, note, nil); err != nil {
			m.logger.Warn("transfer: could not tell the orchestrator about an arrived worker", "sessionID", rec.ID, "orchestrator", orchestrator, "error", err)
		}
	})
}

func transferArrivalNote(id domain.SessionID, t domain.SessionTransfer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Worker %s moved here from %s, where it was session %s.", id, t.Source.Label, t.Source.SessionID)
	if t.DisplayName != "" {
		fmt.Fprintf(&b, " Task: %s.", t.DisplayName)
	}
	fmt.Fprintf(&b, " Branch: %s.", t.Branch)
	if t.IssueID != "" {
		fmt.Fprintf(&b, " Issue: %s.", t.IssueID)
	}
	if len(t.PullRequests) > 0 {
		fmt.Fprintf(&b, " Pull requests: %s.", strings.Join(t.PullRequests, ", "))
	}
	b.WriteString(" It continues the same conversation and reports to you now. Do not respawn its work.")
	return b.String()
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
	if dest.SessionID == "" {
		return domain.SessionMovedTo{}, fmt.Errorf("%w: the destination session is missing", ErrTransferUnsupported)
	}
	if err := store.RecordSessionTransfer(ctx, id, domain.SessionMovedTo{TransferID: transferID, HostID: dest.HostID, Label: dest.Label, SessionID: dest.SessionID}, m.clock()); err != nil {
		return domain.SessionMovedTo{}, err
	}
	moved, _, err := store.SessionMovedTo(ctx, id)
	if err != nil {
		return domain.SessionMovedTo{}, err
	}
	m.removeSourceTransfer(ctx, id, transferID)
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
	return m.transferState(ctx, id)
}

func (m *Manager) sessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error) {
	state, ok, err := m.transferState(ctx, id)
	if err != nil || !ok || state.Pending() {
		return domain.SessionMovedTo{}, false, err
	}
	return state, true, nil
}

// transferState is the session's finished or pending move.
func (m *Manager) transferState(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error) {
	store, ok := m.store.(sessionTransferStore)
	if !ok {
		return domain.SessionMovedTo{}, false, nil
	}
	return store.SessionMovedTo(ctx, id)
}

// refuseMoved keeps a session that continues elsewhere from running here again.
func (m *Manager) refuseMoved(ctx context.Context, id domain.SessionID) error {
	state, ok, err := m.transferState(ctx, id)
	switch {
	case err != nil || !ok:
		return err
	case state.Pending():
		return fmt.Errorf("%w: open Move to another computer to finish the move to %s", ErrTransferPending, state.Label)
	default:
		return fmt.Errorf("%w: continues on %s as %s", ErrSessionMoved, state.Label, state.SessionID)
	}
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
