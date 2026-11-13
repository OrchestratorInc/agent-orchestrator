package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

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
	if _, ok := agent.(ports.AgentNativeSessionConfigProvider); !ok {
		return nativetransfer.ClaudeExportOptions{}, ErrCloudTranscriptUnsupported
	}
	// The session's account decides which config folder holds its conversation.
	configDir, err := m.nativeConfigDir(ctx, rec.Harness, m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env))
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
// finds it from workspacePath and returns its native id.
func (m *Manager) placeTransferConversation(ctx context.Context, harness domain.AgentHarness, workspacePath string, env map[string]string, archive string) (string, error) {
	if harness != domain.HarnessClaudeCode {
		return "", ErrCloudTranscriptUnsupported
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
		return nil, fmt.Errorf("%w: the agent cannot resume conversation %s", ErrCloudTranscriptUnsupported, nativeID)
	}
	return cmd, nil
}
