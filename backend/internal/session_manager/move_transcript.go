package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursorstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/opencodeidentity"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/nativetransfer"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrCloudTranscriptUnsupported reports an agent whose conversation AO Cloud
// cannot resume. Claude Code's, Codex's, OpenCode 2's and Cursor's can be moved.
var ErrCloudTranscriptUnsupported = errors.New("orchestrator cloud cannot resume this agent's conversation")

// ErrNoRecordedConversation reports a session whose agent has not reported its
// conversation id yet, such as one that never answered.
var ErrNoRecordedConversation = errors.New("the session has no recorded agent conversation")

// CloudTranscriptSize returns the size of the transcript a move to AO Cloud
// would carry.
func (m *Manager) CloudTranscriptSize(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (int64, error) {
	if rec.Harness == domain.HarnessOpenCodeV2 {
		data, _, err := m.openCodeExport(ctx, rec, project)
		return int64(len(data)), err
	}
	opts, err := m.cloudTranscriptOptions(ctx, rec, project)
	if err != nil {
		return 0, err
	}
	switch rec.Harness {
	case domain.HarnessCodex:
		_, _, size, err := nativetransfer.CodexRollout(ctx, nativetransfer.CodexOptions{Home: opts.ConfigDir, NativeID: opts.NativeID})
		return size, err
	case domain.HarnessCursor:
		path, err := cursorChat(ctx, opts)
		if err != nil {
			return 0, err
		}
		return cursorChatSize(path), nil
	}
	_, _, size, err := nativetransfer.ClaudeTranscript(ctx, opts)
	return size, err
}

// WriteCloudTranscript writes a stopped session's transcript, with its paths
// replaced by AO Cloud's placeholders, and returns its conversation id.
func (m *Manager) WriteCloudTranscript(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, w io.Writer, maxBytes int64) (string, error) {
	if rec.Harness == domain.HarnessOpenCodeV2 {
		data, id, err := m.openCodeExport(ctx, rec, project)
		if err != nil {
			return "", err
		}
		if int64(len(data)) > maxBytes {
			return "", fmt.Errorf("%w: %d bytes, over %d", nativetransfer.ErrTooLarge, len(data), maxBytes)
		}
		_, err = w.Write(data)
		return id, err
	}
	opts, err := m.cloudTranscriptOptions(ctx, rec, project)
	if err != nil {
		return "", err
	}
	switch rec.Harness {
	case domain.HarnessCodex:
		return nativetransfer.WriteCodexRolloutForCloud(ctx, w, nativetransfer.CodexOptions{Home: opts.ConfigDir, NativeID: opts.NativeID}, maxBytes)
	case domain.HarnessCursor:
		path, err := cursorChat(ctx, opts)
		if err != nil {
			return "", err
		}
		if size := cursorChatSize(path); size > maxBytes {
			return "", fmt.Errorf("%w: %d bytes, over %d", nativetransfer.ErrTooLarge, size, maxBytes)
		}
		archive, err := cursorstore.Pack(ctx, opts.ConfigDir, path, w)
		return archive.ID, err
	}
	return nativetransfer.WriteClaudeTranscriptForCloud(ctx, w, opts, maxBytes)
}

// CloudSidecarInfo sizes the sidecar folder a move to AO Cloud would carry.
func (m *Manager) CloudSidecarInfo(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (nativetransfer.ClaudeSidecar, error) {
	if rec.Harness != domain.HarnessClaudeCode {
		return nativetransfer.ClaudeSidecar{}, nil
	}
	opts, err := m.cloudTranscriptOptions(ctx, rec, project)
	if err != nil {
		return nativetransfer.ClaudeSidecar{}, err
	}
	return nativetransfer.ClaudeSidecarInfo(ctx, opts)
}

// WriteCloudSidecar writes a stopped session's sidecar folder for AO Cloud,
// gzipped, and reports whether it has one.
func (m *Manager) WriteCloudSidecar(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, w io.Writer, maxBytes int64) (bool, error) {
	if rec.Harness != domain.HarnessClaudeCode {
		return false, nil
	}
	opts, err := m.cloudTranscriptOptions(ctx, rec, project)
	if err != nil {
		return false, err
	}
	return nativetransfer.WriteClaudeSidecarForCloud(ctx, w, opts, maxBytes)
}

func (m *Manager) cloudTranscriptOptions(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (nativetransfer.ClaudeExportOptions, error) {
	if rec.Harness != domain.HarnessClaudeCode && rec.Harness != domain.HarnessCodex && rec.Harness != domain.HarnessCursor {
		return nativetransfer.ClaudeExportOptions{}, ErrCloudTranscriptUnsupported
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return nativetransfer.ClaudeExportOptions{}, ErrCloudTranscriptUnsupported
	}
	if _, ok := agent.(ports.AgentNativeSessionConfigProvider); !ok {
		return nativetransfer.ClaudeExportOptions{}, ErrCloudTranscriptUnsupported
	}
	// The session's account decides which config folder holds its conversation.
	configDir, err := m.nativeConfigDir(ctx, rec.Harness, m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env))
	if err != nil {
		return nativetransfer.ClaudeExportOptions{}, err
	}
	nativeID, err := recordedConversation(rec)
	if err != nil {
		return nativetransfer.ClaudeExportOptions{}, err
	}
	return nativetransfer.ClaudeExportOptions{ConfigDir: configDir, NativeID: nativeID, Cwd: rec.Metadata.WorkspacePath}, nil
}

// recordedConversation is the conversation the session recorded. An id
// derived from the session's name is not tried: another install's session of
// the same name can have left a transcript under it.
func recordedConversation(rec domain.SessionRecord) (string, error) {
	if id := rec.Metadata.AgentSessionID; id != "" {
		return id, nil
	}
	if id := rec.Metadata.ProviderConversationID; id != "" {
		return id, nil
	}
	return "", ErrNoRecordedConversation
}

// cursorChat finds the session's Cursor chat store: the terminal CLI's for
// its working directory, the Chat layout's, or a copy in another directory.
func cursorChat(ctx context.Context, opts nativetransfer.ClaudeExportOptions) (string, error) {
	for _, find := range []func() (string, error){
		func() (string, error) { return cursorstore.SessionPath(opts.ConfigDir, opts.Cwd, opts.NativeID) },
		func() (string, error) { return cursorstore.ACPSessionPath(opts.ConfigDir, opts.NativeID) },
	} {
		if path, err := find(); err == nil {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}
	path, found, err := cursorstore.Find(ctx, opts.ConfigDir, opts.NativeID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no Cursor chat %s: %w", opts.NativeID, os.ErrNotExist)
	}
	return path, nil
}

// cursorChatSize is a chat store's size with its WAL.
func cursorChatSize(path string) int64 {
	var size int64
	for _, name := range []string{path, path + "-wal"} {
		if info, err := os.Stat(name); err == nil {
			size += info.Size()
		}
	}
	return size
}

// conversationTransfer is an agent that exports and imports its own
// conversations: OpenCode 2, through `opencode session export` and `import`.
type conversationTransfer interface {
	ExportConversation(ctx context.Context, id string, env map[string]string) ([]byte, error)
	ImportConversation(ctx context.Context, data []byte, cwd string, env map[string]string) (string, error)
}

func (m *Manager) conversationTransfer(harness domain.AgentHarness) (conversationTransfer, error) {
	agent, ok := m.agents.Agent(harness)
	if !ok {
		return nil, ErrCloudTranscriptUnsupported
	}
	t, ok := agent.(conversationTransfer)
	if !ok {
		return nil, ErrCloudTranscriptUnsupported
	}
	return t, nil
}

// openCodeExport is the session's OpenCode 2 conversation with its working
// directory replaced by AO Cloud's placeholder, and its id.
func (m *Manager) openCodeExport(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) ([]byte, string, error) {
	t, err := m.conversationTransfer(rec.Harness)
	if err != nil {
		return nil, "", err
	}
	id, err := recordedConversation(rec)
	if err != nil {
		return nil, "", err
	}
	// A Chat session records the id in the envelope that names its harness.
	if strings.HasPrefix(id, "ao-harness:") {
		if id, err = opencodeidentity.Decode(rec.Harness, id); err != nil {
			return nil, "", err
		}
	}
	data, err := t.ExportConversation(ctx, id, m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env))
	if err != nil {
		return nil, "", err
	}
	data, err = nativetransfer.RewriteOpenCodeExport(data, nativetransfer.CloudCwdPlaceholder)
	return data, id, err
}

func (m *Manager) nativeConfigDir(ctx context.Context, harness domain.AgentHarness, env map[string]string) (string, error) {
	agent, ok := m.agents.Agent(harness)
	if !ok {
		return "", ErrCloudTranscriptUnsupported
	}
	provider, ok := agent.(ports.AgentNativeSessionConfigProvider)
	if !ok {
		return "", ErrCloudTranscriptUnsupported
	}
	return provider.NativeSessionConfigDir(ctx, env)
}

// placeTransferConversation puts a moved conversation where the agent CLI
// finds it from workspacePath and returns its native id. For Codex the
// archive is the rollout itself, for OpenCode 2 its session export document,
// for Cursor a tar of the chat's store.db and meta.json.
func (m *Manager) placeTransferConversation(ctx context.Context, harness domain.AgentHarness, workspacePath string, env map[string]string, archive string) (string, error) {
	if harness == domain.HarnessOpenCodeV2 {
		t, err := m.conversationTransfer(harness)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(archive)
		if err != nil {
			return "", err
		}
		if data, err = nativetransfer.RewriteOpenCodeExport(data, workspacePath); err != nil {
			return "", err
		}
		return t.ImportConversation(ctx, data, workspacePath, env)
	}
	if harness != domain.HarnessClaudeCode && harness != domain.HarnessCodex && harness != domain.HarnessCursor {
		return "", ErrCloudTranscriptUnsupported
	}
	configDir, err := m.nativeConfigDir(ctx, harness, env)
	if err != nil {
		return "", err
	}
	if harness == domain.HarnessCodex {
		return nativetransfer.ImportCodex(ctx, archive, nativetransfer.CodexImportOptions{Home: configDir, Cwd: workspacePath, BackupDir: m.moveBackupDir("codex")})
	}
	if harness == domain.HarnessCursor {
		f, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		return cursorstore.Unpack(ctx, configDir, f, workspacePath)
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
	// ImportClaude keeps a transcript already in place, as a repeated import
	// should. For a move that copy predates the moved conversation, so
	// resuming it would drop every turn taken since: the moved one counts as
	// not placed, and the session starts from the note while the source keeps
	// the full copy.
	if res.Existing {
		return "", fmt.Errorf("a conversation %s is already in place here; the moved one was not placed over it", res.NativeID)
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
		return nil, fmt.Errorf("%w: the agent cannot resume conversation %s", ErrCloudTranscriptUnsupported, nativeID)
	}
	return cmd, nil
}

// moveBackupDir is where a move keeps a harness's conversation it replaced
// here (under the daemon's data dir, ~/.ao), or "" without a data dir: the
// import then refuses to replace one.
func (m *Manager) moveBackupDir(harness string) string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "move-backups", harness)
}
