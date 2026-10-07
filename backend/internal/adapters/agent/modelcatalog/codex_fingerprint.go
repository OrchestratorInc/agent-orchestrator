package modelcatalog

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
)

func codexDiscoveryFingerprint(workingDir string, env map[string]string) string {
	root, _ := nativeconfig.Resolve(env, "CODEX_HOME", ".codex")
	if root != "" && !filepath.IsAbs(root) {
		root = filepath.Join(workingDir, root)
	}
	paths := []string{filepath.Join(root, "config.toml"), filepath.Join(root, "auth.json")}
	if workingDir != "" {
		for dir := filepath.Clean(workingDir); ; dir = filepath.Dir(dir) {
			paths = append(paths, filepath.Join(dir, ".codex", "config.toml"))
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil || filepath.Dir(dir) == dir {
				break
			}
		}
	}
	keys := map[string]bool{"OPENAI_API_KEY": true, "OPENAI_BASE_URL": true, "CODEX_API_KEY": true}
	for _, path := range paths {
		raw, err := readModelConfig(path)
		if err != nil || filepath.Ext(path) != ".toml" {
			continue
		}
		var config struct {
			Providers map[string]struct {
				EnvKey string `toml:"env_key"`
			} `toml:"model_providers"`
			ModelCatalogJSON string `toml:"model_catalog_json"`
		}
		if toml.Unmarshal(raw, &config) != nil {
			continue
		}
		for _, provider := range config.Providers {
			if provider.EnvKey != "" {
				keys[provider.EnvKey] = true
			}
		}
		if config.ModelCatalogJSON != "" {
			catalogPath := config.ModelCatalogJSON
			if !filepath.IsAbs(catalogPath) {
				catalogPath = filepath.Join(filepath.Dir(path), catalogPath)
			}
			paths = append(paths, catalogPath)
		}
	}
	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	hash := sha256.New()
	_, _ = hash.Write([]byte(root + "\x00" + fingerprintConfigPaths(paths)))
	for _, key := range orderedKeys {
		_, _ = hash.Write([]byte("\x00" + key + "\x00" + envValue(env, key)))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}
