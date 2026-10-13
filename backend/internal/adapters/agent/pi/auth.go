package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus returns the plugin's local authentication status.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if status, ok, err := piLocalAuthStatus(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	} else if ok {
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

// piLocalAuthStatus checks Pi's own auth.json first; a key in the environment
// is the fallback when it holds no usable credential.
func piLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	status, ok, err := piStoredAuthStatus(ctx)
	if err == nil && ok && (status == ports.AgentAuthStatusAuthorized || status == ports.AgentAuthStatusConfigured) {
		return status, true, nil
	}
	if name := authprobe.FirstCredentialEnvironment(ctx, []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY",
		"MISTRAL_API_KEY", "GROQ_API_KEY", "XAI_API_KEY", "OPENROUTER_API_KEY",
		"DEEPSEEK_API_KEY", "ZAI_API_KEY", "CEREBRAS_API_KEY", "KIMI_API_KEY",
	}); name != "" {
		ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodAPIKey))
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return status, ok, err
}

func piStoredAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	configDir, ok := piConfigDir()
	if !ok {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	path := filepath.Join(configDir, "auth.json")
	status, method, ok, err := piAuthJSON(ctx, path)
	if status == ports.AgentAuthStatusAuthorized {
		evidence := domain.FileSource(path).WithMethod(method)
		evidence.Provider = authprobe.JSONProviderNames(path)
		ports.RecordAuthEvidence(ctx, evidence)
	}
	return status, ok, err
}

func piConfigDir() (string, bool) {
	if configDir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); configDir != "" {
		return configDir, true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".pi", "agent"), true
}

type piAuthEntry struct {
	Type    string  `json:"type"`
	Key     string  `json:"key"`
	Access  string  `json:"access"`
	Refresh string  `json:"refresh"`
	Expires float64 `json:"expires"`
}

func piAuthJSONStatus(path string) (ports.AgentAuthStatus, bool, error) {
	status, _, ok, err := piAuthJSON(context.Background(), path)
	return status, ok, err
}

// piAuthJSON also reports how the stored credential signs in.
func piAuthJSON(ctx context.Context, path string) (ports.AgentAuthStatus, domain.AgentAccountMethod, bool, error) {
	data, err := authprobe.ReadCredentialFile(ctx, path)
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, "", false, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, "", false, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return ports.AgentAuthStatusUnknown, "", false, nil
	}

	var entries map[string]piAuthEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_file_invalid")
		return ports.AgentAuthStatusUnknown, "", false, err
	}
	if len(entries) == 0 {
		return ports.AgentAuthStatusUnknown, "", false, nil
	}
	for provider, entry := range entries {
		if strings.TrimSpace(provider) == "" {
			continue
		}
		if piAuthEntryIsOAuth(entry) {
			return ports.AgentAuthStatusAuthorized, domain.AgentAccountMethodAccount, true, nil
		}
		if key := strings.TrimSpace(entry.Key); strings.HasPrefix(key, "!") {
			authprobe.RecordFileDiagnostic(ctx, path, "credential_command_deferred")
		} else if key != "" && !piAuthKeyIsResolved(key) {
			authprobe.RecordFileDiagnostic(ctx, path, "credential_reference_unresolved")
		}
		if piAuthKeyIsResolved(entry.Key) {
			return ports.AgentAuthStatusAuthorized, domain.AgentAccountMethodAPIKey, true, nil
		}
	}
	return ports.AgentAuthStatusUnknown, "", false, nil
}

func piAuthEntryIsOAuth(entry piAuthEntry) bool {
	if !strings.EqualFold(strings.TrimSpace(entry.Type), "oauth") {
		return false
	}
	// OAuth tokens live in auth.json after `/login` and auto-refresh when
	// expired, so a stored access token counts as a login even past expiry.
	// `/logout` deletes the provider entry from auth.json rather than leaving
	// a stale token behind, so a lingering access string is not a logout case.
	return strings.TrimSpace(entry.Access) != ""
}

func piAuthKeyIsResolved(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "!") {
		// Avoid executing configured commands during a global auth probe. Their
		// result remains unknown until Pi resolves them while launching.
		return false
	}

	resolved := true
	expanded := os.Expand(key, func(name string) string {
		switch name {
		case "$", "!":
			// Pi documents $$ and $! as escaped literal prefixes.
			return name
		default:
			value, ok := os.LookupEnv(name)
			if !ok || strings.TrimSpace(value) == "" {
				resolved = false
			}
			return value
		}
	})
	return resolved && strings.TrimSpace(expanded) != ""
}
