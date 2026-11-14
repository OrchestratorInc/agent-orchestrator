package modelcatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func init() { browseProviderCredentials["kimchi"] = kimchiConfiguredProviders }

const kimchiProviderID = "kimchi-dev"

func kimchiConfiguredProviders(_ string, env map[string]string) []string {
	home := catalogHome(env)
	if home == "" {
		return nil
	}
	dir := filepath.Join(home, ".config", "kimchi", "harness")
	found := map[string]bool{}
	for _, provider := range piAuthStoreProviders(filepath.Join(dir, "auth.json"), env) {
		found[provider] = true
	}
	declared := piModelsConfigProviders(filepath.Join(dir, "models.json"), env)
	for _, provider := range declared.configured {
		found[provider] = true
	}
	if piCredentialEnv(env, "KIMCHI_API_KEY") != "" || kimchiConfigHasAPIKey(filepath.Join(home, ".config", "kimchi", "config.json")) {
		found[kimchiProviderID] = true
		for _, provider := range declared.all {
			if strings.HasPrefix(provider, kimchiProviderID+"/") {
				found[provider] = true
			}
		}
	}
	return sortedProviderSet(found)
}

func kimchiConfigHasAPIKey(path string) bool {
	raw, err := readModelConfig(path)
	if err != nil {
		return false
	}
	var config struct {
		APIKey      string `json:"apiKey"`
		SnakeAPIKey string `json:"api_key"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return false
	}
	return strings.TrimSpace(config.APIKey) != "" || strings.TrimSpace(config.SnakeAPIKey) != ""
}

func piCredentialEnv(env map[string]string, name string) string {
	return credentialPresence(envValue(env, name))
}

func piAuthStoreProviders(path string, env map[string]string) []string {
	raw, err := readModelConfig(path)
	if err != nil {
		return nil
	}
	var entries map[string]struct {
		Type    string `json:"type"`
		Key     string `json:"key"`
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var providers []string
	for provider, entry := range entries {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			continue
		}
		switch entry.Type {
		case "oauth":
			if strings.TrimSpace(entry.Access) != "" || strings.TrimSpace(entry.Refresh) != "" {
				providers = append(providers, provider)
			}
		case "api", "api_key":
			if piKeyResolves(entry.Key, env, false) {
				providers = append(providers, provider)
			}
		}
	}
	return providers
}

type piDeclaredProviders struct {
	all        []string
	configured []string
}

func piModelsConfigProviders(path string, env map[string]string) piDeclaredProviders {
	var result piDeclaredProviders
	raw, err := readModelConfig(path)
	if err != nil {
		return result
	}
	var config struct {
		Providers map[string]struct {
			APIKey string `json:"apiKey"`
		} `json:"providers"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return result
	}
	for provider, entry := range config.Providers {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			continue
		}
		result.all = append(result.all, provider)
		if piKeyResolves(entry.APIKey, env, true) {
			result.configured = append(result.configured, provider)
		}
	}
	return result
}

var piEnvNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func piKeyResolves(value string, env map[string]string, bareNames bool) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "!") {
		return false
	}
	if bareNames && piEnvNamePattern.MatchString(value) {
		return piCredentialEnv(env, value) != ""
	}
	resolved := true
	expanded := os.Expand(value, func(name string) string {
		if name == "$" || name == "!" {
			return name
		}
		v := piCredentialEnv(env, name)
		if v == "" {
			resolved = false
		}
		return v
	})
	return resolved && strings.TrimSpace(expanded) != ""
}

func sortedProviderSet(found map[string]bool) []string {
	providers := make([]string, 0, len(found))
	for provider := range found {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}
