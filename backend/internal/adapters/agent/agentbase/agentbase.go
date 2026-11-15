package agentbase

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func ModelConfigSpec(ctx context.Context, description string) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{Fields: []ports.ConfigField{{
		Key: "model", Type: ports.ConfigFieldString, Description: description,
	}}}, nil
}

func AppendModelFlag(cmd *[]string, cfg ports.AgentConfig, flag string) {
	if model := strings.TrimSpace(cfg.Model); model != "" {
		*cmd = append(*cmd, flag, model)
	}
}

func ModelEffortConfigSpec(ctx context.Context, modelDescription, effortDescription string) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{Fields: []ports.ConfigField{
		{Key: "model", Type: ports.ConfigFieldString, Description: modelDescription},
		{Key: "effort", Type: ports.ConfigFieldString, Description: effortDescription},
	}}, nil
}

func AppendEffortFlag(cmd *[]string, cfg ports.AgentConfig, flag string) {
	if effort := strings.TrimSpace(cfg.Effort); effort != "" {
		*cmd = append(*cmd, flag, effort)
	}
}

type Base struct{}

func (Base) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, ctx.Err()
}

func (Base) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

func (Base) GetAgentHooks(ctx context.Context, _ ports.WorkspaceHookConfig) error {
	return ctx.Err()
}

func (Base) GetRestoreCommand(ctx context.Context, _ ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func (Base) SessionInfo(ctx context.Context, _ ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	return ports.SessionInfo{}, false, nil
}

func StandardSessionInfo(session ports.SessionRef) (ports.SessionInfo, bool) {
	info := ports.SessionInfo{
		AgentSessionID: session.Metadata[ports.MetadataKeyAgentSessionID],
		Title:          session.Metadata[ports.MetadataKeyTitle],
		Summary:        session.Metadata[ports.MetadataKeySummary],
	}
	if info.AgentSessionID == "" && info.Title == "" && info.Summary == "" {
		return ports.SessionInfo{}, false
	}
	return info, true
}
