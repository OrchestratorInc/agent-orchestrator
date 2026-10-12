package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentAuthChecker        = (*Plugin)(nil)
	_ ports.AgentAuthAccountChecker = (*Plugin)(nil)
)

// AuthStatus returns the plugin's local authentication status.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, err := p.AuthStatusWithAccount(ctx)
	return status, err
}

// AuthStatusWithAccount also names the account `whoami` reports.
func (p *Plugin) AuthStatusWithAccount(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, error) {
	binary, err := p.kiroBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, err
	}
	// Kiro's own login, as `kiro-cli whoami` reports it, decides first;
	// KIRO_API_KEY in the environment is the fallback.
	status, account, err := kiroWhoamiAuth(ctx, binary)
	if err == nil && status == ports.AgentAuthStatusAuthorized {
		return status, account, nil
	}
	if strings.TrimSpace(os.Getenv("KIRO_API_KEY")) != "" {
		return ports.AgentAuthStatusAuthorized, domain.EnvironmentSource("KIRO_API_KEY").WithMethod(domain.AgentAccountMethodAPIKey), nil
	}
	return status, account, err
}

func kiroWhoamiAuthStatus(ctx context.Context, binary string) (ports.AgentAuthStatus, error) {
	status, _, err := kiroWhoamiAuth(ctx, binary)
	return status, err
}

func kiroWhoamiAuth(ctx context.Context, binary string) (ports.AgentAuthStatus, domain.AgentAccountIdentity, error) {
	if binary == "" {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil
	}
	// Kiro documents `whoami` as its authentication-status command. Keep the
	// probe bounded so catalog refresh cannot hang on a broken CLI install.
	var account domain.AgentAccountIdentity
	status, err := authprobe.CLIStatusWithClassifier(
		ctx, binary, [][]string{{"whoami", "--format", "json"}}, func(out []byte) (ports.AgentAuthStatus, bool) {
			status, ok := kiroStatusFromWhoami(out)
			if ok && status == ports.AgentAuthStatusAuthorized {
				account = kiroWhoamiAccount(out)
			}
			return status, ok
		})
	if status != ports.AgentAuthStatusAuthorized {
		account = domain.AgentAccountIdentity{}
	}
	return status, account, err
}

// kiroWhoamiAccount reads the email from a signed-in `whoami`, inline or
// under "account". The start URL and profile ARN are provider identity.
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

// kiroStatusFromWhoami reads `kiro-cli whoami --format json`.
//
// Signed in, Kiro prints a JSON identity object followed by a plain-text
// "Profile:" block, so the payload as a whole is not valid JSON and the shared
// prose classifier matched nothing in it — which is why a valid login reported
// as unknown (#5048). Decoding only the leading value skips that trailer.
//
// The signed-out shape (`{"account":null}`) is handled by the shared
// classifier, so this reports only what it can positively recognize and leaves
// everything else to fall back.
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
	// Builds that report the identity inline rather than under "account".
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
