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
	// ErrTransferNotPending refuses to finish a move that is no longer pending.
	ErrTransferNotPending = errors.New("this move was cancelled on the source computer, where the session still runs")
	// ErrTransferUnconfirmed reports a receive whose session may still run here.
	ErrTransferUnconfirmed = errors.New("the session may have started on that computer")
	// ErrTransferDefaultBranch refuses a session working on the default branch.
	ErrTransferDefaultBranch = errors.New("the session works on the project's default branch; moving it would push that branch to origin")
)

// transferStaleAfter is when an unfinished transfer's files are swept.
const transferStaleAfter = 7 * 24 * time.Hour

var (
	transferIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)
	commitSHAPattern  = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

type sessionTransferStore interface {
	RecordPendingTransfer(ctx context.Context, id domain.SessionID, dest domain.SessionMovedTo, at time.Time) error
	CancelPendingTransfer(ctx context.Context, id domain.SessionID, transferID string) (bool, error)
	FinishPendingTransfer(ctx context.Context, id domain.SessionID, moved domain.SessionMovedTo, at time.Time) (bool, error)
	SessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error)
}

func (m *Manager) transferDir(transferID string) (string, error) {
	if !transferIDPattern.MatchString(transferID) {
		return "", fmt.Errorf("%w: invalid transfer id", ErrTransferUnsupported)
	}
	return filepath.Join(m.dataDir, "transfers", transferID), nil
}

func (m *Manager) transferStore() (sessionTransferStore, error) {
	store, ok := m.store.(sessionTransferStore)
	if !ok {
		return nil, fmt.Errorf("%w: store cannot record transfers", ErrTransferUnsupported)
	}
	return store, nil
}

// lockTransfer waits for other work on the same session's move, or on the same
// received transfer, to end.
func (m *Manager) lockTransfer(ctx context.Context, key string) (unlock func(), err error) {
	done := make(chan struct{})
	for {
		other, busy := m.transferLocks.LoadOrStore(key, done)
		if !busy {
			return func() { m.transferLocks.Delete(key); close(done) }, nil
		}
		otherDone, _ := other.(chan struct{})
		select {
		case <-otherDone:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func preservedTransferRef(transferID string) string { return "refs/ao/preserved/" + transferID }

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

// PrepareTransfer marks the session as being moved to dest, so it cannot run
// here until CommitTransfer or CancelTransfer, then stops its agent and puts
// what the destination needs into one file in the data dir: a git bundle of
// the work origin may not have, and the agent's conversation when it fits. A failure removes the
// mark. Calling it again with the same transfer id, or for an unfinished move
// to the same project, returns the earlier result. A mid-turn agent is refused
// unless stopTurn is set, which ends the turn.
func (m *Manager) PrepareTransfer(ctx context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination, stopTurn bool) (_ domain.SessionTransfer, err error) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	store, err := m.transferStore()
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	unlock, err := m.lockTransfer(ctx, "session "+string(id))
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	defer unlock()
	if t, ok, err := m.readTransferPayload(id, transferID); err != nil || ok {
		return t, err
	}
	if state, ok, err := m.transferState(ctx, id); err != nil {
		return domain.SessionTransfer{}, err
	} else if ok && state.Pending() {
		if state.HostID != dest.HostID || state.ProjectID != dest.ProjectID {
			return domain.SessionTransfer{}, fmt.Errorf("%w: finish the move to %s first", ErrTransferPending, state.Label)
		}
		if t, ok, err := m.readTransferPayload(id, state.TransferID); err != nil || ok {
			return t, err
		}
	}
	m.sweepTransfers()
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
	if err := git.CheckTransfer(ctx, workspaceInfo(rec)); err != nil {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: %w", id, err)
	}
	if !stopTurn && (rec.Activity.State == domain.ActivityActive || rec.Activity.State == domain.ActivityBlocked) {
		return domain.SessionTransfer{}, ErrTransferAgentBusy
	}
	if err := store.RecordPendingTransfer(ctx, id, domain.SessionMovedTo{TransferID: transferID, HostID: dest.HostID, Label: dest.Label, ProjectID: dest.ProjectID}, m.clock()); err != nil {
		return domain.SessionTransfer{}, err
	}
	defer func() {
		if err == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		m.removeSourceTransfer(transferID)
		if _, cancelErr := store.CancelPendingTransfer(ctx, id, transferID); cancelErr != nil {
			m.logger.Warn("transfer: could not release the session", "sessionID", id, "error", cancelErr)
		}
	}()
	if _, err := m.ExitAgent(ctx, id); err != nil && !errors.Is(err, ErrAgentExited) {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: stop agent: %w", id, err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return domain.SessionTransfer{}, err
	}
	var head string
	var uncommitted, bundled bool
	err = func() error {
		// A new session's branch is renamed under this gate once its title is
		// generated; read the branch and bundle it without a rename in between.
		defer m.acquireWorkspaceGate(rec.ProjectID)()
		if rec, err = m.getRecord(ctx, id); err != nil {
			return err
		}
		ws := workspaceInfo(rec)
		localRef, err := m.workspace.StashUncommitted(ctx, ws)
		if err != nil {
			return fmt.Errorf("transfer %s: capture uncommitted work: %w", id, err)
		}
		if head, uncommitted, bundled, err = git.BundleTransfer(ctx, ws, localRef, preservedTransferRef(transferID), filepath.Join(dir, transferBundleName)); err != nil {
			return fmt.Errorf("transfer %s: bundle: %w", id, err)
		}
		return nil
	}()
	if err != nil {
		return domain.SessionTransfer{}, err
	}
	native, err := m.exportTransferConversation(ctx, rec, project, filepath.Join(dir, transferConversationName))
	if err != nil {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: export conversation: %w", id, err)
	}
	archive, withConversation, err := packTransfer(dir)
	if err != nil {
		return domain.SessionTransfer{}, fmt.Errorf("transfer %s: %w", id, err)
	}
	if native != nil && !withConversation {
		m.logger.Warn("transfer: the conversation does not fit; the destination starts from the handoff note", "sessionID", id)
		native = nil
	}
	remoteRef := ""
	if uncommitted {
		remoteRef = preservedTransferRef(transferID)
	}
	t := domain.SessionTransfer{
		TransferID:      transferID,
		Source:          domain.SessionTransferSource{SessionID: id},
		RepoKey:         domain.RepositoryKey(project.RepoOriginURL),
		Branch:          rec.Metadata.Branch,
		HeadSHA:         head,
		PreservedRef:    remoteRef,
		Archive:         archive,
		Bundle:          bundled,
		Native:          native,
		Mode:            rec.Mode,
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
	return t, nil
}

// CancelTransfer forgets an unfinished move so the session can run here again,
// and removes what the move left behind. The agent stays stopped.
func (m *Manager) CancelTransfer(ctx context.Context, id domain.SessionID, transferID string) error {
	if _, err := m.transferDir(transferID); err != nil {
		return err
	}
	store, err := m.transferStore()
	if err != nil {
		return err
	}
	unlock, err := m.lockTransfer(ctx, "session "+string(id))
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := store.CancelPendingTransfer(ctx, id, transferID); err != nil {
		return err
	}
	m.removeSourceTransfer(transferID)
	return nil
}

// removeSourceTransfer deletes a transfer's files. A failure is logged: the
// move itself is already settled.
func (m *Manager) removeSourceTransfer(transferID string) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		m.logger.Warn("transfer: could not remove transfer files", "transferID", transferID, "error", err)
	}
}

// sweepTransfers removes transfer files left by moves that never finished.
// ponytail: runs on the next prepare or receive; add a daemon-start sweep if they pile up.
func (m *Manager) sweepTransfers() {
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
		// Uploads for a receive are redone on every attempt, so they go stale sooner.
		staleAfter := transferStaleAfter
		if _, err := os.Stat(filepath.Join(root, e.Name(), "payload.json")); errors.Is(err, os.ErrNotExist) {
			staleAfter = time.Hour
		}
		if m.clock().Sub(info.ModTime()) >= staleAfter {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

func transferable(rec domain.SessionRecord, project domain.ProjectRecord) error {
	switch {
	case rec.IsTerminated:
		return fmt.Errorf("%w: it has ended", ErrTransferUnsupported)
	case rec.Kind != domain.KindWorker:
		return fmt.Errorf("%w: only workers can move", ErrTransferUnsupported)
	case rec.ProjectID == "" || project.Kind.WithDefault() != domain.ProjectKindSingleRepo:
		return fmt.Errorf("%w: only single-repository projects can move", ErrTransferUnsupported)
	case rec.Metadata.Branch == "" || rec.Metadata.WorkspacePath == "":
		return fmt.Errorf("%w: the session has no worktree branch", ErrTransferUnsupported)
	}
	return nil
}

// exportTransferConversation writes the agent's own conversation to archive
// and describes it. Only Claude Code's can move; another agent, or a session
// whose agent never wrote one, exports none and starts from the handoff note.
func (m *Manager) exportTransferConversation(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, archive string) (*domain.SessionTransferNative, error) {
	if rec.Harness != domain.HarnessClaudeCode {
		return nil, nil
	}
	configDir, err := m.nativeConfigDir(ctx, rec.Harness, m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env))
	if err != nil {
		m.logger.Warn("transfer: no conversation to move; the destination starts from the handoff note", "sessionID", rec.ID, "error", err)
		return nil, nil
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
	return &domain.SessionTransferNative{NativeID: manifest.NativeID, SourceCwd: manifest.SourceCwd, CLIVersion: manifest.CLIVersion}, nil
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

// TransferArchivePath is the file session id's prepared transfer carries.
func (m *Manager) TransferArchivePath(id domain.SessionID, transferID string) (string, error) {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return "", err
	}
	if _, ok, err := m.readTransferPayload(id, transferID); err != nil || !ok {
		return "", fmt.Errorf("%w: no prepared transfer %s for session %s", ErrTransferArchive, transferID, id)
	}
	path := filepath.Join(dir, transferArchiveName)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("%w: %w", ErrTransferArchive, err)
	}
	return path, nil
}

// StoreTransferArchive keeps the file uploaded for a transfer, replacing an
// earlier upload.
func (m *Manager) StoreTransferArchive(transferID string, r io.Reader) error {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	n, err := io.Copy(tmp, io.LimitReader(r, MaxTransferArchiveBytes+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > MaxTransferArchiveBytes {
		err = fmt.Errorf("%w: larger than %d bytes", ErrTransferArchive, MaxTransferArchiveBytes)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, transferArchiveName))
}

// openTransferArchive checks the uploaded file against the transfer and
// unpacks it.
func (m *Manager) openTransferArchive(transferID string, archive domain.SessionTransferArchive) error {
	dir, err := m.transferDir(transferID)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, transferArchiveName)
	size, sum, err := fileDigest(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: the work and conversation were not uploaded", ErrTransferArchive)
	}
	if err != nil {
		return err
	}
	if size != archive.Size || sum != archive.SHA256 {
		return fmt.Errorf("%w: size or checksum does not match", ErrTransferArchive)
	}
	return unpackTransfer(path, dir)
}

// ReceiveTransfer starts a session here that continues a transfer prepared
// on another machine. prompt is the first turn of the resumed conversation.
// A retry with the same transfer id waits for a running attempt and returns the
// session it started; an attempt that was cut off is ended and done again.
func (m *Manager) ReceiveTransfer(ctx context.Context, projectID domain.ProjectID, t domain.SessionTransfer, prompt string) (rec domain.SessionRecord, err error) {
	dir, err := m.transferDir(t.TransferID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	unlock, err := m.lockTransfer(ctx, "receive "+t.TransferID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	defer unlock()
	defer func() {
		// A cut off receive is retried with the same upload.
		if err != nil && ctx.Err() == nil && !errors.Is(err, ErrTransferUnconfirmed) {
			_ = os.RemoveAll(dir)
		}
	}()
	if !commitSHAPattern.MatchString(t.HeadSHA) {
		return domain.SessionRecord{}, fmt.Errorf("%w: invalid head %q", ErrTransferUnsupported, t.HeadSHA)
	}
	if ref, ok := strings.CutPrefix(t.PreservedRef, "refs/ao/preserved/"); t.PreservedRef != "" && (!ok || !transferIDPattern.MatchString(ref)) {
		return domain.SessionRecord{}, fmt.Errorf("%w: invalid preserved ref %q", ErrTransferUnsupported, t.PreservedRef)
	}
	existing, clientRequestID, err := m.receivedSession(ctx, t)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	if existing.ID != "" {
		_ = os.RemoveAll(dir)
		return existing, nil
	}
	m.sweepTransfers()
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	if project.ID == "" {
		return domain.SessionRecord{}, fmt.Errorf("%w: the project no longer exists on that computer", ErrTransferUnsupported)
	}
	if project.Kind.WithDefault() != domain.ProjectKindSingleRepo {
		return domain.SessionRecord{}, fmt.Errorf("%w: only single-repository projects can receive a session", ErrTransferUnsupported)
	}
	if key := domain.RepositoryKey(project.RepoOriginURL); key == "" || key != t.RepoKey {
		return domain.SessionRecord{}, ErrTransferRepoMismatch
	}
	if t.Archive == nil && (t.Bundle || t.Native != nil) {
		return domain.SessionRecord{}, fmt.Errorf("%w: the transfer names no file", ErrTransferArchive)
	}
	bundle, conversation := "", ""
	if t.Archive != nil {
		if err := m.openTransferArchive(t.TransferID, *t.Archive); err != nil {
			return domain.SessionRecord{}, err
		}
		if t.Bundle {
			bundle = filepath.Join(dir, transferBundleName)
		}
		if t.Native != nil {
			conversation = filepath.Join(dir, transferConversationName)
		}
	}
	git, ok := m.workspace.(ports.WorkspaceTransfer)
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("%w: workspace adapter cannot transfer", ErrTransferUnsupported)
	}
	if err := git.FetchTransfer(ctx, project.Path, t.Branch, t.HeadSHA, bundle, t.PreservedRef); err != nil {
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
		ProjectID: projectID,
		IssueID:   t.IssueID,
		Kind:      domain.KindWorker,
		Harness:   t.Harness,
		Branch:    t.Branch,
		// Spawn resumes a moved conversation in the terminal; without one the
		// agent starts from the handoff note in the mode it had.
		RequestedMode:     t.Mode,
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
			NativeArchive:  conversation,
			FallbackPrompt: t.HandoffNote,
		},
	})
	if err != nil {
		// Nothing may run here once this fails: the source resumes the session.
		started, _, lookupErr := m.receivedSession(context.WithoutCancel(ctx), t)
		if lookupErr != nil {
			return domain.SessionRecord{}, fmt.Errorf("%w: %w", lookupErr, err)
		}
		if started.ID == "" {
			return domain.SessionRecord{}, err
		}
		rec = started
	}
	_ = os.RemoveAll(dir)
	m.tellOrchestratorAboutArrival(ctx, rec, t)
	return rec, nil
}

// receivedSession is the session an earlier receive of t started, or the
// request id for starting it. Attempts that did not finish are ended.
func (m *Manager) receivedSession(ctx context.Context, t domain.SessionTransfer) (domain.SessionRecord, string, error) {
	requestID := "transfer-" + t.TransferID
	for attempt := 2; ; attempt++ {
		rec, found, err := m.store.GetSessionByClientRequestID(ctx, requestID)
		if err != nil {
			return domain.SessionRecord{}, "", fmt.Errorf("%w: %w", ErrTransferUnconfirmed, err)
		}
		if !found {
			return domain.SessionRecord{}, requestID, nil
		}
		replay, err := replayClientRequest(rec, transferRequestHash(t))
		if !errors.Is(err, ErrClientRequestIncomplete) {
			return replay, requestID, err
		}
		if !rec.IsTerminated {
			if _, err := m.Kill(ctx, rec.ID); err != nil && !errors.Is(err, ErrTerminated) {
				return domain.SessionRecord{}, "", fmt.Errorf("%w: %w", ErrTransferUnconfirmed, err)
			}
		}
		m.releaseTransferBranch(ctx, rec)
		requestID = fmt.Sprintf("transfer-%s-%d", t.TransferID, attempt)
	}
}

// releaseTransferBranch frees the branch an ended session's worktree still holds.
func (m *Manager) releaseTransferBranch(ctx context.Context, rec domain.SessionRecord) {
	git, ok := m.workspace.(ports.WorkspaceTransfer)
	if !ok || !dirExists(rec.Metadata.WorkspacePath) {
		return
	}
	if err := git.ReleaseTransferBranch(ctx, workspaceInfo(rec)); err != nil {
		m.logger.Warn("transfer: could not release the branch", "sessionID", rec.ID, "error", err)
	}
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
// project is told, so it does not start the work again. Only a pending move to
// dest can be committed; committing it twice succeeds.
func (m *Manager) CommitTransfer(ctx context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination) (domain.SessionMovedTo, error) {
	store, err := m.transferStore()
	if err != nil {
		return domain.SessionMovedTo{}, err
	}
	if _, err := m.transferDir(transferID); err != nil {
		return domain.SessionMovedTo{}, err
	}
	unlock, err := m.lockTransfer(ctx, "session "+string(id))
	if err != nil {
		return domain.SessionMovedTo{}, err
	}
	defer unlock()
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionMovedTo{}, err
	}
	if dest.SessionID == "" {
		return domain.SessionMovedTo{}, fmt.Errorf("%w: the destination session is missing", ErrTransferUnsupported)
	}
	moved := domain.SessionMovedTo{TransferID: transferID, HostID: dest.HostID, Label: dest.Label, ProjectID: dest.ProjectID, SessionID: dest.SessionID}
	if ok, err := store.FinishPendingTransfer(ctx, id, moved, m.clock()); err != nil {
		return domain.SessionMovedTo{}, err
	} else if !ok {
		return domain.SessionMovedTo{}, ErrTransferNotPending
	}
	m.removeSourceTransfer(transferID)
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
	// The session can later move back to this computer.
	m.releaseTransferBranch(ctx, rec)
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
		return fmt.Errorf("%w: the move to %s is not finished; use Finish move or Keep it here on the session", ErrTransferPending, state.Label)
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
