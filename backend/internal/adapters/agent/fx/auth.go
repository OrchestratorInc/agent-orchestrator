package fx

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const defaultStatusTimeout = 3 * time.Second

type commandRunner func(context.Context, string, ...string) ([]byte, error)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus first reads fx's local credential state, then verifies configured
// credentials through its provider-backed model catalog. `fx status` alone
// proves only that a credential exists, not that the provider accepts it.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	binary, err := p.fxBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}

	timeout := p.statusTimeout
	if timeout <= 0 {
		timeout = defaultStatusTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runner := p.statusRunner
	if runner == nil {
		runner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return aoprocess.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	output, _ := runner(probeCtx, binary, "status", "--json")
	if probeCtx.Err() != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return ports.AgentAuthStatusUnknown, nil
		}
		return ports.AgentAuthStatusUnknown, probeCtx.Err()
	}
	localStatus := authStatusFromJSON(output)
	if localStatus != ports.AgentAuthStatusConfigured {
		return localStatus, nil
	}

	output, runErr := runner(probeCtx, binary, "models", "--json")
	if probeCtx.Err() != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ports.AgentAuthStatusUnknown, ctxErr
		}
		return localStatus, nil
	}
	status := authStatusFromModelsJSON(output)
	if status == ports.AgentAuthStatusUnauthorized {
		return status, nil
	}
	if runErr != nil {
		return localStatus, nil
	}
	if status == ports.AgentAuthStatusAuthorized {
		return status, nil
	}
	return localStatus, nil
}

func authStatusFromJSON(output []byte) ports.AgentAuthStatus {
	var status struct {
		Auth        string `json:"auth"`
		AuthExpired bool   `json:"auth_expired"`
	}
	if err := json.Unmarshal(output, &status); err != nil {
		return ports.AgentAuthStatusUnknown
	}
	auth := strings.TrimSpace(status.Auth)
	if status.AuthExpired || strings.EqualFold(auth, "missing") {
		return ports.AgentAuthStatusUnauthorized
	}
	if auth != "" {
		return ports.AgentAuthStatusConfigured
	}
	return ports.AgentAuthStatusUnknown
}

func authStatusFromModelsJSON(output []byte) ports.AgentAuthStatus {
	var response struct {
		Kind  string `json:"kind"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return ports.AgentAuthStatusUnknown
	}
	if strings.EqualFold(strings.TrimSpace(response.Code), "AuthenticationRejected") ||
		strings.Contains(strings.ToLower(response.Error), "authenticationrejected") {
		return ports.AgentAuthStatusUnauthorized
	}
	if strings.EqualFold(strings.TrimSpace(response.Kind), "models") && strings.TrimSpace(response.Error) == "" {
		return ports.AgentAuthStatusAuthorized
	}
	return ports.AgentAuthStatusUnknown
}
