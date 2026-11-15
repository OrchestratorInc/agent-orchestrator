package commandcode

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

const commandCodeAuthProbeTimeout = 5 * time.Second

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.binary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	return commandCodeAuthStatus(ctx, binary)
}

func commandCodeAuthStatus(ctx context.Context, binary string) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if strings.TrimSpace(binary) == "" {
		return ports.AgentAuthStatusUnknown, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, commandCodeAuthProbeTimeout)
	defer cancel()
	out, _ := authprobe.CmdRunner(runCtx, binary, "status")
	status := commandCodeAuthStatusFromOutput(string(out))
	if status.HasCredential() {
		ports.RecordAuthEvidence(ctx, domain.CommandSource(binary, "status"))
	}
	return status, nil
}

func commandCodeAuthStatusFromOutput(out string) ports.AgentAuthStatus {
	text := strings.ToLower(out)
	if strings.Contains(text, "not verified") {
		return ports.AgentAuthStatusUnauthorized
	}
	status := authprobe.StatusFromText(out)
	if status == ports.AgentAuthStatusUnknown && strings.Contains(text, "verified") {
		return ports.AgentAuthStatusAuthorized
	}
	return status
}
