package amp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus returns the plugin's local authentication status.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	// Amp's own stored login and settings decide first, then `amp usage`,
	// and only then AMP_API_KEY in the environment.
	status, ok, login, storedErr := ampStoredAuthStatus(ctx)
	if storedErr == nil && ok && status == ports.AgentAuthStatusAuthorized {
		if login {
			// A stored login names no account; Amp's own account list does.
			ports.AddAuthEvidence(ctx, domain.AgentAccountIdentity{Email: ampActiveAccountEmail(ctx, binary)})
		}
		return status, nil
	}
	if storedErr == nil && !ok {
		usage, err := ampUsageAuthStatus(ctx, binary)
		if err != nil {
			return ports.AgentAuthStatusUnknown, err
		}
		if usage == ports.AgentAuthStatusAuthorized {
			return usage, nil
		}
	}
	if envStatus, envOK := ampEnvAuthStatus(ctx); envOK {
		return envStatus, nil
	}
	if storedErr != nil {
		return ports.AgentAuthStatusUnknown, storedErr
	}
	return status, nil
}

// ampActiveAccountEmail reads the account `amp account list` marks active
// ("* user@example.com (user_…)"), or "" when Amp cannot say.
func ampActiveAccountEmail(ctx context.Context, binary string) string {
	if binary == "" {
		return ""
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := authprobe.CmdRunner(probeCtx, binary, "account", "list")
	if err != nil || probeCtx.Err() != nil {
		return ""
	}
	return ampActiveEmailFromList(string(out))
}

func ampActiveEmailFromList(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "*") {
			continue
		}
		for _, field := range strings.Fields(strings.TrimPrefix(trimmed, "*")) {
			if strings.Contains(field, "@") {
				return strings.Trim(field, `"'()<>,`)
			}
		}
	}
	return ""
}

// ampLocalAuthStatus checks Amp's stored login and settings, then
// AMP_API_KEY in the environment.
func ampLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	status, ok, _, err := ampStoredAuthStatus(ctx)
	if err == nil && ok && status == ports.AgentAuthStatusAuthorized {
		return status, true, nil
	}
	if envStatus, envOK := ampEnvAuthStatus(ctx); envOK {
		return envStatus, true, nil
	}
	return status, ok, err
}

func ampEnvAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool) {
	if strings.TrimSpace(os.Getenv("AMP_API_KEY")) != "" {
		ports.RecordAuthEvidence(ctx, domain.EnvironmentSource("AMP_API_KEY").WithMethod(domain.AgentAccountMethodAPIKey))
		return ports.AgentAuthStatusAuthorized, true
	}
	return ports.AgentAuthStatusUnknown, false
}

// ampStoredAuthStatus reads Amp's secrets file, then its settings. login
// reports that the stored login, not a settings key, decided.
func ampStoredAuthStatus(ctx context.Context) (status ports.AgentAuthStatus, ok, login bool, err error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, false, err
	}
	secretsPath := ampSecretsPath()
	if status, ok, err := ampSecretsAuthStatus(secretsPath); err != nil || ok {
		if status == ports.AgentAuthStatusAuthorized {
			ports.RecordAuthEvidence(ctx, domain.FileSource(secretsPath).WithMethod(domain.AgentAccountMethodAccount))
		}
		return status, ok, true, err
	}
	settingsPath := ampSettingsPath()
	status, ok, err = ampSettingsAuthStatus(settingsPath)
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, false, err
	}
	if status == ports.AgentAuthStatusAuthorized {
		ports.RecordAuthEvidence(ctx, domain.FileSource(settingsPath).WithMethod(domain.AgentAccountMethodAPIKey))
	}
	return status, ok, false, nil
}

// Amp documents its persisted CLI credentials in ~/.local/share/amp/secrets.json.
// The schema has changed across CLI releases, so only credential-shaped fields
// are considered; arbitrary settings in the file must not imply authentication.
func ampSecretsPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "amp", "secrets.json")
	}
	return ""
}

func ampSecretsAuthStatus(path string) (ports.AgentAuthStatus, bool, error) {
	if path == "" {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	for key, value := range values {
		lower := strings.ToLower(key)
		if !strings.Contains(lower, "token") && !strings.Contains(lower, "secret") && !strings.Contains(lower, "key") {
			continue
		}
		if strings.TrimSpace(stringValue(value)) != "" {
			return ports.AgentAuthStatusAuthorized, true, nil
		}
	}
	return ports.AgentAuthStatusUnknown, true, nil
}

func ampSettingsPath() string {
	if path := strings.TrimSpace(os.Getenv("AMP_SETTINGS_FILE")); path != "" {
		return expandHome(path)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "amp", "settings.json")
	}
	return ""
}

func ampSettingsAuthStatus(path string) (ports.AgentAuthStatus, bool, error) {
	if path == "" {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.AgentAuthStatusUnknown, false, nil
		}
		return ports.AgentAuthStatusUnknown, false, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	for _, key := range []string{"amp.apiKey", "amp.api_key", "apiKey", "api_key"} {
		if value, ok := settings[key]; ok {
			if strings.TrimSpace(stringValue(value)) != "" {
				return ports.AgentAuthStatusAuthorized, true, nil
			}
			return ports.AgentAuthStatusUnknown, false, nil
		}
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

// ampUsageAuthStatus recognizes Amp's own signed-in account output. It is
// deliberately authorization-only: a failed or unfamiliar usage command does
// not prove that an interactive launch cannot authenticate.
func ampUsageAuthStatus(ctx context.Context, binary string) (ports.AgentAuthStatus, error) {
	if binary == "" {
		return ports.AgentAuthStatusUnknown, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := authprobe.CmdRunner(probeCtx, binary, "usage", "--no-color")
	if probeCtx.Err() != nil {
		if probeCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return ports.AgentAuthStatusUnknown, nil
		}
		return ports.AgentAuthStatusUnknown, probeCtx.Err()
	}
	if err == nil && strings.Contains(strings.ToLower(string(out)), "signed in as") {
		evidence := domain.CommandSource(binary, "usage").WithMethod(domain.AgentAccountMethodAccount)
		evidence.Email = authprobe.SignedInEmail(string(out))
		ports.RecordAuthEvidence(ctx, evidence)
		return ports.AgentAuthStatusAuthorized, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
