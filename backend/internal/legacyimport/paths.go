package legacyimport

import (
	"os"
	"path/filepath"
)

var userHomeDir = os.UserHomeDir

func DefaultLegacyRootDir() string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agent-orchestrator")
}

func globalConfigPath(root string) string {
	return filepath.Join(root, "config.yaml")
}

func preferencesPath(root string) string {
	return filepath.Join(root, "portfolio", "preferences.json")
}

func registeredPath(root string) string {
	return filepath.Join(root, "portfolio", "registered.json")
}
