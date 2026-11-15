package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentAuthChecker            = (*Plugin)(nil)
	_ ports.AgentAuthDiagnosticsChecker = (*Plugin)(nil)
)

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	binary, err := p.kiroBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}
	status, account, steps, err := kiroWhoamiAuthDiagnostics(ctx, binary)
	if err != nil && ctx.Err() != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, steps, err
	}
	if err == nil && status == ports.AgentAuthStatusAuthorized {
		return status, account, steps, nil
	}
	if name := authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, "KIRO_API_KEY"); name != "" {
		return ports.AgentAuthStatusAuthorized, domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodAPIKey), steps, nil
	}
	return status, account, steps, err
}

func kiroWhoamiAuthDiagnostics(ctx context.Context, binary string) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	if binary == "" {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, nil
	}
	var account domain.AgentAccountIdentity
	status, steps, err := authprobe.CLIStatusWithDiagnostics(
		ctx, binary, [][]string{{"whoami", "--format", "json"}}, 3*time.Second, func(out []byte) (ports.AgentAuthStatus, bool) {
			status, ok := kiroStatusFromWhoami(out)
			if ok && status == ports.AgentAuthStatusAuthorized {
				account = kiroWhoamiAccount(out)
			}
			return status, ok
		})
	if status != ports.AgentAuthStatusAuthorized {
		account = domain.AgentAccountIdentity{}
	}
	return status, account, steps, err
}

func kiroWhoamiAccount(out []byte) domain.AgentAccountIdentity {
	var payload struct {
		Email   string `json:"email"`
		Account *struct {
			Email string `json:"email"`
		} `json:"account"`
	}
	if json.NewDecoder(bytes.NewReader(out)).Decode(&payload) != nil {
		return domain.AgentAccountIdentity{}
	}
	email := payload.Email
	if email == "" && payload.Account != nil {
		email = payload.Account.Email
	}
	return domain.AgentAccountIdentity{Email: email, Method: domain.AgentAccountMethodAccount}
}

func kiroStatusFromWhoami(out []byte) (ports.AgentAuthStatus, bool) {
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&payload); err != nil {
		return "", false
	}
	if raw, ok := payload["account"]; ok {
		if isJSONNull(raw) {
			return ports.AgentAuthStatusUnauthorized, true
		}
		return ports.AgentAuthStatusAuthorized, true
	}
	for _, key := range []string{"email", "accountType", "startUrl"} {
		if raw, ok := payload[key]; ok && !isJSONNull(raw) && !isBlankJSONString(raw) {
			return ports.AgentAuthStatusAuthorized, true
		}
	}
	return "", false
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func isBlankJSONString(raw json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	return strings.TrimSpace(s) == ""
}
