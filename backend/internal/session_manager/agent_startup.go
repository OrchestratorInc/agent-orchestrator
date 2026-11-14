package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type startupTerminal struct {
	runtime runtimeController
	handle  ports.RuntimeHandle
}

func (t startupTerminal) Output(ctx context.Context) (string, error) {
	reader, ok := t.runtime.(ports.StyledTerminalOutputReader)
	if !ok {
		return "", errors.New("native initialization requires styled terminal output")
	}
	return reader.GetStyledOutput(ctx, t.handle, 100)
}

func (t startupTerminal) SendInput(ctx context.Context, input string) error {
	writer, ok := t.runtime.(interface {
		SendInput(context.Context, ports.RuntimeHandle, string) error
	})
	if !ok {
		return errors.New("native initialization requires raw terminal input")
	}
	return writer.SendInput(ctx, t.handle, input)
}

func (m *Manager) initializeAgentTerminal(ctx context.Context, agent ports.Agent, cfg ports.LaunchConfig, handle ports.RuntimeHandle) error {
	initializer, ok := agent.(ports.AgentStartupInitializer)
	if !ok {
		return nil
	}
	if err := initializer.InitializeTerminal(ctx, cfg, startupTerminal{runtime: m.runtime, handle: handle}); err != nil {
		m.logger.Warn("native terminal initialization failed", "sessionID", cfg.SessionID, "error", agentlaunch.RedactError(err, cfg.Env))
		return fmt.Errorf("initialize native terminal: %w", err)
	}
	return nil
}

func acquireAgentStartup(ctx context.Context, agent ports.Agent, cfg ports.LaunchConfig) (func(), error) {
	guard, ok := agent.(ports.AgentStartupGuard)
	if !ok {
		return func() {}, nil
	}
	return guard.BeginStartup(ctx, cfg)
}
