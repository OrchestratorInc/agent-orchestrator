package modelcatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var qwenDefaultKeyEnv = map[string]string{
	"openai": "OPENAI_API_KEY", "openai-responses": "OPENAI_API_KEY",
	"anthropic": "ANTHROPIC_API_KEY", "gemini": "GEMINI_API_KEY", "vertex-ai": "GOOGLE_API_KEY",
}

func init() { browseProviderCredentials["qwen"] = qwenConfiguredProviders }

type qwenSettings struct {
	Env            map[string]any `json:"env"`
	ModelProviders map[string][]struct {
		EnvKey string `json:"envKey"`
		APIKey string `json:"apiKey"`
	} `json:"modelProviders"`
	Security struct {
		Auth struct {
			SelectedType string `json:"selectedType"`
			APIKey       string `json:"apiKey"`
		} `json:"auth"`
	} `json:"security"`
}

func qwenConfiguredProviders(workingDir string, env map[string]string) []string {
	home := credentialPresence(env["HOME"])
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	qwenHome := qwenConfigHome(home, env)
	found := map[string]bool{}
	if qwenHome != "" {
		if info, err := os.Stat(filepath.Join(qwenHome, "oauth_creds.json")); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			found["qwen-oauth"] = true
		}
	}
	keyNames := map[string][]string{}
	for provider, name := range qwenDefaultKeyEnv {
		keyNames[provider] = append(keyNames[provider], name)
	}
	settingsEnv := map[string]string{}
	for _, path := range modelConfigPaths("qwen", workingDir, env) {
		raw, err := readModelConfig(path)
		if err != nil {
			continue
		}
		var settings qwenSettings
		if json.Unmarshal(raw, &settings) != nil {
			continue
		}
		for name, value := range settings.Env {
			if text, ok := value.(string); ok && credentialPresence(text) != "" {
				settingsEnv[name] = text
			}
		}
		for provider, entries := range settings.ModelProviders {
			for _, entry := range entries {
				if strings.TrimSpace(entry.APIKey) != "" {
					found[provider] = true
				}
				if name := strings.TrimSpace(entry.EnvKey); name != "" {
					keyNames[provider] = append(keyNames[provider], name)
				}
			}
		}
		if auth := settings.Security.Auth; strings.TrimSpace(auth.SelectedType) != "" && strings.TrimSpace(auth.APIKey) != "" {
			found[strings.TrimSpace(auth.SelectedType)] = true
		}
	}
	var envFiles []string
	if qwenHome != "" {
		envFiles = append(envFiles, filepath.Join(qwenHome, ".env"))
	}
	if home != "" {
		envFiles = append(envFiles, filepath.Join(home, ".env"))
	}
	present := func(name string) bool {
		if value, ok := env[name]; ok {
			return credentialPresence(value) != ""
		}
		if credentialPresence(os.Getenv(name)) != "" || settingsEnv[name] != "" {
			return true
		}
		for _, path := range envFiles {
			if envFileHasKey(path, name) {
				return true
			}
		}
		return false
	}
	for provider, names := range keyNames {
		for _, name := range names {
			if present(name) {
				found[provider] = true
				break
			}
		}
	}
	providers := make([]string, 0, len(found))
	for provider := range found {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

func uniqueProviders(providers []string) []string {
	if len(providers) == 0 {
		return providers
	}
	seen := make(map[string]bool, len(providers))
	out := providers[:0]
	for _, provider := range providers {
		if !seen[provider] {
			seen[provider] = true
			out = append(out, provider)
		}
	}
	return out
}
