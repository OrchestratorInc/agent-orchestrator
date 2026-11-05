package primeagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus checks Prime Agent's documented credential sources without
// starting a session or making a provider request.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return ports.AgentAuthStatusUnknown, nil
		}
		return ports.AgentAuthStatusUnknown, err
	}
	status, ok, err := primeLocalAuthStatus(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if ok {
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

var primeAPIKeyEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_OAUTH_TOKEN",
	"AZURE_OPENAI_API_KEY",
	"OPENAI_API_KEY",
	"PRIME_API_KEY",
	"DEEPSEEK_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_CLOUD_API_KEY",
	"MISTRAL_API_KEY",
	"GROQ_API_KEY",
	"CEREBRAS_API_KEY",
	"CLOUDFLARE_API_KEY",
	"XAI_API_KEY",
	"OPENROUTER_API_KEY",
	"AI_GATEWAY_API_KEY",
	"ZAI_API_KEY",
	"OPENCODE_API_KEY",
	"HF_TOKEN",
	"FIREWORKS_API_KEY",
	"KIMI_API_KEY",
	"MINIMAX_API_KEY",
	"MINIMAX_CN_API_KEY",
	"XIAOMI_API_KEY",
	"XIAOMI_TOKEN_PLAN_CN_API_KEY",
	"XIAOMI_TOKEN_PLAN_AMS_API_KEY",
	"XIAOMI_TOKEN_PLAN_SGP_API_KEY",
	"AWS_BEARER_TOKEN_BEDROCK",
}

// primeLocalAuthStatus checks Prime Agent's own auth.json and models.json
// first; keys and cloud credentials in the environment are the fallback when
// those hold no usable credential.
func primeLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	status, found, err := primeStoredAuthStatus(ctx)
	if err == nil && found && status.HasCredential() {
		return status, true, nil
	}
	for _, name := range primeAPIKeyEnvVars {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			method := domain.AgentAccountMethodAPIKey
			if strings.Contains(name, "TOKEN") && !strings.HasSuffix(name, "_API_KEY") {
				method = domain.AgentAccountMethodToken
			}
			ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(name).WithMethod(method))
			return ports.AgentAuthStatusAuthorized, true, nil
		}
	}
	if primeAWSCredentialEnvConfigured() {
		ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(primeFirstSetEnv("AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_ROLE_ARN", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI")).WithMethod(domain.AgentAccountMethodAccount))
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	if primeGoogleCredentialConfigured() {
		ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(primeFirstSetEnv("GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID", "GCLOUD_PROJECT")).WithMethod(domain.AgentAccountMethodAccount))
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return status, found, err
}

func primeStoredAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	dir, ok := primeConfigDir()
	if !ok {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	for _, file := range []struct {
		name          string
		allowPlainKey bool
	}{{"auth.json", true}, {"models.json", false}} {
		path := filepath.Join(dir, file.name)
		status, found, data, err := primeCredentialJSONStatus(path, file.allowPlainKey)
		if err != nil || found {
			if status == ports.AgentAuthStatusAuthorized {
				ports.RecordAuthEvidence(ctx, primeFileEvidence(path, data, file.name == "models.json"))
			}
			return status, found, err
		}
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

// primeFileEvidence names a credential file and the providers it holds.
// auth.json is keyed by provider; models.json nests them under "providers"
// and holds API keys.
func primeFileEvidence(path string, data []byte, models bool) domain.AgentAccountIdentity {
	if models {
		var config struct {
			Providers json.RawMessage `json:"providers"`
		}
		_ = json.Unmarshal(data, &config)
		evidence := authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAPIKey)
		evidence.Provider = authprobe.JSONProviderNames(config.Providers)
		return evidence
	}
	evidence := authprobe.FileSource(path).WithMethod(authprobe.JSONProviderMethod(data))
	evidence.Provider = authprobe.JSONProviderNames(data)
	return evidence
}

// primeFirstSetEnv names the first of names that is set, for display.
func primeFirstSetEnv(names ...string) string {
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return name
		}
	}
	return names[0]
}

func primeConfigDir() (string, bool) {
	for _, name := range []string{primeAgentCodingAgentDirEnv, "PI_CODING_AGENT_DIR"} {
		if dir := strings.TrimSpace(os.Getenv(name)); dir != "" {
			return dir, true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".prime", "agent"), true
}

func primeAWSCredentialEnvConfigured() bool {
	if strings.TrimSpace(os.Getenv("AWS_PROFILE")) != "" ||
		strings.TrimSpace(os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI")) != "" ||
		strings.TrimSpace(os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")) != "" &&
		strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY")) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")) != "" &&
		strings.TrimSpace(os.Getenv("AWS_ROLE_ARN")) != ""
}

func primeGoogleCredentialConfigured() bool {
	project := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
	if project == "" {
		project = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT_ID"))
	}
	if project == "" {
		project = strings.TrimSpace(os.Getenv("GCLOUD_PROJECT"))
	}
	if project == "" || strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_LOCATION")) == "" {
		return false
	}
	if path := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); path != "" {
		return primeNonEmptyRegularFile(path)
	}
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return primeNonEmptyRegularFile(filepath.Join(configHome, "gcloud", "application_default_credentials.json"))
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		return primeNonEmptyRegularFile(filepath.Join(appData, "gcloud", "application_default_credentials.json"))
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	return primeNonEmptyRegularFile(filepath.Join(home, ".config", "gcloud", "application_default_credentials.json"))
}

func primeNonEmptyRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// primeCredentialJSONStatus also returns the file's contents, so callers
// parse it without reading it again.
func primeCredentialJSONStatus(path string, allowPlainKey bool) (ports.AgentAuthStatus, bool, []byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Prime Agent's own credential file
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, false, nil, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return ports.AgentAuthStatusUnknown, false, nil, nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return ports.AgentAuthStatusUnknown, false, nil, err
	}
	if primeJSONHasCredential(value, allowPlainKey) {
		return ports.AgentAuthStatusAuthorized, true, data, nil
	}
	return ports.AgentAuthStatusUnknown, false, nil, nil
}

func primeJSONHasCredential(value any, allowPlainKey bool) bool {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			if primeJSONHasCredential(child, allowPlainKey) {
				return true
			}
		}
	case map[string]any:
		for key, child := range value {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "mcp:") {
				continue
			}
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
				switch normalized {
				case "apikey", "access", "refresh", "accesstoken", "refreshtoken", "oauthtoken":
					return true
				case "key":
					if allowPlainKey {
						return true
					}
				}
			}
			if primeJSONHasCredential(child, allowPlainKey) {
				return true
			}
		}
	}
	return false
}
