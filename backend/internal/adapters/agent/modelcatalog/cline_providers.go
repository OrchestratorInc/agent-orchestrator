package modelcatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func init() { browseProviderCredentials["cline"] = clineConfiguredProviders }

// clineConfiguredProviders reports the Cline providers whose stored settings
// hold a credential: an API key, a refresh token, or an unexpired access
// token. IDs are the providers.json keys, the same IDs Cline's configured
// catalog groups models under. CLINE_API_KEY, which Cline accepts in place of
// a stored account login, marks the cline provider. Provider keys Cline could
// read from other environment variables are not counted.
func clineConfiguredProviders(_ string, env map[string]string) []string {
	lookup := func(name string) string {
		if value, ok := env[name]; ok {
			return credentialPresence(value)
		}
		return credentialPresence(os.Getenv(name))
	}
	var providers []string
	if lookup("CLINE_API_KEY") != "" {
		providers = append(providers, "cline")
	}
	raw, err := readModelConfig(clineProviderSettingsPath(lookup))
	if err != nil {
		return providers
	}
	var file struct {
		Providers map[string]struct {
			Settings struct {
				APIKey    string `json:"apiKey"`
				APIKeyAlt string `json:"apikey"`
				Auth      *struct {
					AccessToken  string `json:"accessToken"`
					RefreshToken string `json:"refreshToken"`
					ExpiresAt    int64  `json:"expiresAt"`
				} `json:"auth"`
			} `json:"settings"`
		} `json:"providers"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return providers
	}
	now := time.Now().UnixMilli()
	for id, provider := range file.Providers {
		id = strings.TrimSpace(id)
		settings := provider.Settings
		stored := strings.TrimSpace(settings.APIKey) != "" || strings.TrimSpace(settings.APIKeyAlt) != ""
		if auth := settings.Auth; auth != nil {
			stored = stored || strings.TrimSpace(auth.RefreshToken) != "" ||
				(strings.TrimSpace(auth.AccessToken) != "" && (auth.ExpiresAt <= 0 || auth.ExpiresAt > now))
		}
		if id != "" && stored {
			providers = append(providers, id)
		}
	}
	return providers
}

// clineProviderSettingsPath mirrors Cline's own resolution:
// CLINE_PROVIDER_SETTINGS_PATH, then CLINE_DATA_DIR/settings, then
// CLINE_DIR (default ~/.cline)/data/settings.
func clineProviderSettingsPath(lookup func(string) string) string {
	if path := lookup("CLINE_PROVIDER_SETTINGS_PATH"); path != "" {
		return path
	}
	dataDir := lookup("CLINE_DATA_DIR")
	if dataDir == "" {
		root := lookup("CLINE_DIR")
		if root == "" {
			home := lookup("HOME")
			if home == "" {
				home, _ = os.UserHomeDir()
			}
			if home == "" {
				return ""
			}
			root = filepath.Join(home, ".cline")
		}
		dataDir = filepath.Join(root, "data")
	}
	return filepath.Join(dataDir, "settings", "providers.json")
}
