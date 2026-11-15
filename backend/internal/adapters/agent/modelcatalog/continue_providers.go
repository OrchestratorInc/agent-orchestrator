package modelcatalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

func init() { browseProviderCredentials["continue"] = continueConfiguredProviders }

var continueSecretRef = regexp.MustCompile(`^\$\{\{\s*secrets\.([^\s}]+)\s*\}\}$`)

func continueConfiguredProviders(workingDir string, env map[string]string) []string {
	lookup := func(name string) string {
		if value, ok := env[name]; ok {
			return credentialPresence(value)
		}
		return credentialPresence(os.Getenv(name))
	}
	home := continueHomeDir(lookup)
	if home == "" {
		return nil
	}
	raw, err := readModelConfig(filepath.Join(home, "config.yaml"))
	if err != nil {
		return nil
	}
	var config struct {
		Models []struct {
			Provider string `yaml:"provider"`
			APIKey   string `yaml:"apiKey"`
		} `yaml:"models"`
	}
	if yaml.Unmarshal(raw, &config) != nil {
		return nil
	}
	envFiles := []string{filepath.Join(home, ".env")}
	if workingDir != "" {
		envFiles = append(envFiles, filepath.Join(workingDir, ".continue", ".env"), filepath.Join(workingDir, ".env"))
	}
	resolvable := func(apiKey string) bool {
		apiKey = strings.TrimSpace(apiKey)
		match := continueSecretRef.FindStringSubmatch(apiKey)
		if match == nil {
			return apiKey != "" && !strings.Contains(apiKey, "${{")
		}
		name := match[1][strings.LastIndex(match[1], "/")+1:]
		if lookup(name) != "" {
			return true
		}
		for _, path := range envFiles {
			if envFileHasKey(path, name) {
				return true
			}
		}
		return false
	}
	var providers []string
	for _, model := range config.Models {
		if provider := strings.TrimSpace(model.Provider); provider != "" && resolvable(model.APIKey) {
			providers = append(providers, provider)
		}
	}
	return uniqueProviders(providers)
}

func continueHomeDir(lookup func(string) string) string {
	if dir := lookup("CONTINUE_GLOBAL_DIR"); dir != "" {
		return dir
	}
	home := lookup("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".continue")
}
