package modelcatalog

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func init() { browseProviderCredentials["vibe"] = vibeConfiguredProviders }

func vibeConfiguredProviders(workingDir string, env map[string]string) []string {
	home, _ := os.UserHomeDir()
	vibeHome := strings.TrimSpace(env["VIBE_HOME"])
	if vibeHome == "" && home != "" {
		vibeHome = filepath.Join(home, ".vibe")
	}
	present := func(name string) bool {
		if value, ok := env[name]; ok {
			return credentialPresence(value) != ""
		}
		if credentialPresence(os.Getenv(name)) != "" {
			return true
		}
		return vibeHome != "" && envFileHasKey(filepath.Join(vibeHome, ".env"), name)
	}
	var providers []string
	for _, path := range modelConfigPaths("vibe", workingDir, env) {
		raw, err := readModelConfig(path)
		if err != nil {
			continue
		}
		var config struct {
			Providers []struct {
				Name         string `toml:"name"`
				APIKeyEnvVar string `toml:"api_key_env_var"`
			} `toml:"providers"`
		}
		if toml.Unmarshal(raw, &config) != nil {
			continue
		}
		for _, provider := range config.Providers {
			name, keyVar := strings.TrimSpace(provider.Name), strings.TrimSpace(provider.APIKeyEnvVar)
			if name != "" && keyVar != "" && present(keyVar) {
				providers = append(providers, name)
			}
		}
	}
	return uniqueProviders(providers)
}
