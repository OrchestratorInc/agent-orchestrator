package modelcatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func init() { browseProviderCredentials["cline"] = clineConfiguredProviders }

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
