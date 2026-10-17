package modelcatalog

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func init() { browseProviderCredentials["kimi"] = kimiConfiguredProviders }

type kimiProviderConfig struct {
	Providers map[string]struct {
		APIKey string            `json:"api_key" toml:"api_key"`
		Env    map[string]string `json:"env" toml:"env"`
		OAuth  *struct {
			Key string `json:"key" toml:"key"`
		} `json:"oauth" toml:"oauth"`
	} `json:"providers" toml:"providers"`
}

// kimiConfiguredProviders reports the [providers.<id>] entries of Kimi's
// config.toml or config.json that hold a credential: an api_key, a key or
// token in the provider's env table, or a managed OAuth login whose
// credentials file has a token. Both the Kimi CLI home (KIMI_SHARE_DIR,
// ~/.kimi) and the Kimi Code home (KIMI_CODE_HOME, ~/.kimi-code) are read.
func kimiConfiguredProviders(_ string, env map[string]string) []string {
	home := catalogHome(env)
	found := map[string]bool{}
	for _, root := range []string{kimiHome(env, "KIMI_SHARE_DIR", home, ".kimi"), kimiHome(env, "KIMI_CODE_HOME", home, ".kimi-code")} {
		if root == "" {
			continue
		}
		for _, name := range []string{"config.toml", "config.json"} {
			for _, provider := range kimiConfigProviders(root, name) {
				found[provider] = true
			}
		}
	}
	return sortedProviderSet(found)
}

func kimiHome(env map[string]string, override, home, name string) string {
	if dir := envValue(env, override); dir != "" {
		return dir
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, name)
}

func kimiConfigProviders(root, name string) []string {
	raw, err := readModelConfig(filepath.Join(root, name))
	if err != nil {
		return nil
	}
	var config kimiProviderConfig
	if name == "config.json" {
		err = json.Unmarshal(raw, &config)
	} else {
		err = toml.Unmarshal(raw, &config)
	}
	if err != nil {
		return nil
	}
	var providers []string
	for provider, entry := range config.Providers {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			continue
		}
		configured := credentialPresence(entry.APIKey) != ""
		for key, value := range entry.Env {
			key = strings.ToUpper(strings.TrimSpace(key))
			if credentialPresence(value) != "" && (strings.HasSuffix(key, "_API_KEY") || strings.HasSuffix(key, "_TOKEN")) {
				configured = true
			}
		}
		if !configured && entry.OAuth != nil {
			configured = kimiOAuthHasToken(root, entry.OAuth.Key)
		}
		if configured {
			providers = append(providers, provider)
		}
	}
	return providers
}

// kimiOAuthHasToken reads <home>/credentials/<key>.json, where Kimi stores a
// managed login named by the provider's oauth.key ("oauth/<name>").
func kimiOAuthHasToken(root, key string) bool {
	name := filepath.Base(filepath.FromSlash(strings.TrimPrefix(strings.TrimSpace(key), "oauth/")))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return false
	}
	raw, err := readModelConfig(filepath.Join(root, "credentials", name+".json"))
	if err != nil {
		return false
	}
	var credentials struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(raw, &credentials) != nil {
		return false
	}
	return strings.TrimSpace(credentials.AccessToken) != "" || strings.TrimSpace(credentials.RefreshToken) != ""
}
