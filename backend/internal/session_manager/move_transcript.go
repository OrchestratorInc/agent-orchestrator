package sessionmanager

import (
	"context"
	"errors"
	"io"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/nativetransfer"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrCloudTranscriptUnsupported reports an agent whose conversation AO Cloud
// cannot resume. Only Claude Code's can be moved today.
var ErrCloudTranscriptUnsupported = errors.New("orchestrator cloud cannot resume this agent's conversation")

// ErrNoRecordedConversation reports a session whose agent has not reported its
// conversation id yet, such as one that never answered.
var ErrNoRecordedConversation = errors.New("the session has no recorded agent conversation")

// CloudTranscriptSize returns the size of the transcript a move to AO Cloud
// would carry.
func (m *Manager) CloudTranscriptSize(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (int64, error) {
	opts, err := m.cloudTranscriptOptions(ctx, rec, project)
	if err != nil {
		return 0, err
	}
	_, _, size, err := nativetransfer.ClaudeTranscript(ctx, opts)
	return size, err
}

// WriteCloudTranscript writes a stopped session's transcript, with its paths
// replaced by AO Cloud's placeholders, and returns its Claude session id.
func (m *Manager) WriteCloudTranscript(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, w io.Writer, maxBytes int64) (string, error) {
	opts, err := m.cloudTranscriptOptions(ctx, rec, project)
	if err != nil {
		return "", err
	}
	return nativetransfer.WriteClaudeTranscriptForCloud(ctx, w, opts, maxBytes)
}

func (m *Manager) cloudTranscriptOptions(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (nativetransfer.ClaudeExportOptions, error) {
	if rec.Harness != domain.HarnessClaudeCode {
		return nativetransfer.ClaudeExportOptions{}, ErrCloudTranscriptUnsupported
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return nativetransfer.ClaudeExportOptions{}, ErrCloudTranscriptUnsupported
	}
	provider, ok := agent.(ports.AgentNativeSessionConfigProvider)
	if !ok {
		return nativetransfer.ClaudeExportOptions{}, ErrCloudTranscriptUnsupported
	}
	// The session's account decides which config folder holds its conversation.
	configDir, err := provider.NativeSessionConfigDir(ctx, m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env))
	if err != nil {
		return nativetransfer.ClaudeExportOptions{}, err
	}
	// Only the conversation the session recorded. An id derived from the
	// session's name is not tried: another install's session of the same name
	// can have left a transcript under it.
	nativeID := rec.Metadata.AgentSessionID
	if nativeID == "" {
		nativeID = rec.Metadata.ProviderConversationID
	}
	if nativeID == "" {
		return nativetransfer.ClaudeExportOptions{}, ErrNoRecordedConversation
	}
	return nativetransfer.ClaudeExportOptions{ConfigDir: configDir, NativeID: nativeID, Cwd: rec.Metadata.WorkspacePath}, nil
}
