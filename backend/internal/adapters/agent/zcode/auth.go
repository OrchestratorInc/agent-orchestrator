package zcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus reports a cheap local authentication probe for ZCode. The CLI's
// documented personal provider config is the only file-backed credential
// surface: provider rules carry an access entry (e.g. an API key). A signed-in
// account credential lives in ZCode's own credential store, which has no
// documented local status file, so this probe reports Unknown rather than
// guessing. Credential values are never read into the result.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	path, ok, err := personalProviderConfigPath()
	if err != nil || !ok {
		return ports.AgentAuthStatusUnknown, err
	}
	configured, err := providerConfigHasAccess(path)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if !configured {
		return ports.AgentAuthStatusUnknown, nil
	}
	ports.RecordAuthEvidence(ctx, domain.AgentAccountIdentity{
		SourceKind: domain.AgentCredentialSourceFile,
		Source:     sourceLabel(path),
		Method:     domain.AgentAccountMethodAPIKey,
	})
	return ports.AgentAuthStatusAuthorized, nil
}

// personalProviderConfigPath resolves the personal provider config file the
// CLI documents: ZCODE_PERSONAL_PROVIDER_CONFIG_FILE wins, then
// <ZCODE_DATA_BASE_DIR|~/.zcode>/v2/provider_config.json. ok is false when no
// base directory exists, so a missing install stays Unknown instead of erroring.
func personalProviderConfigPath() (path string, ok bool, err error) {
	if override := strings.TrimSpace(os.Getenv("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE")); override != "" {
		return override, true, nil
	}
	base := strings.TrimSpace(os.Getenv("ZCODE_DATA_BASE_DIR"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, fmt.Errorf("zcode: resolve home directory: %w", err)
		}
		base = filepath.Join(home, ".zcode")
	}
	path = filepath.Join(base, "v2", "provider_config.json")
	if _, err := os.Stat(path); err != nil {
		return "", false, nil //nolint:nilerr // no base directory: nothing is configured, not an error
	}
	return path, true, nil
}

// providerConfigHasAccess reports whether any provider rule carries an access
// entry. The file may name env expansions or credential values; only the
// presence of an access entry is inspected, and no value is returned.
func providerConfigHasAccess(path string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the user's own ZCode provider config path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("zcode: read %s: %w", path, err)
	}
	var config struct {
		Config struct {
			ProviderConfigRules struct {
				ProviderRules []struct {
					Config struct {
						Access struct {
							Type string `json:"type"`
						} `json:"access"`
					} `json:"config"`
				} `json:"providerRules"`
			} `json:"providerConfigRules"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		// A config this probe cannot parse is not evidence of anything.
		return false, nil //nolint:nilerr // unparsable config is Unknown, not an error
	}
	for _, rule := range config.Config.ProviderConfigRules.ProviderRules {
		if strings.TrimSpace(rule.Config.Access.Type) != "" {
			return true, nil
		}
	}
	return false, nil
}

// sourceLabel renders the credential source as a home-relative path. It never
// contains the file's contents.
func sourceLabel(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Base(path)
	}
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
