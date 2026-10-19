package cline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveACPProvider preserves an explicit provider override, otherwise reading
// Cline's saved selection. ACP restores the first available OAuth login rather
// than lastUsedProvider, which can silently select a different billing account.
// Credentials remain owned by Cline; AO only passes the selected provider ID.
func ResolveACPProvider(ctx context.Context, workspacePath string, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	getenv := func(key string) string {
		if value, ok := env[key]; ok {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(os.Getenv(key))
	}
	if provider := getenv("CLINE_PROVIDER"); provider != "" {
		return provider, nil
	}
	// Match Cline's provider-settings path precedence, including isolated CLI
	// profiles. Resolve relative overrides against the provider's working dir.
	path := getenv("CLINE_PROVIDER_SETTINGS_PATH")
	if path == "" {
		dataDir := getenv("CLINE_DATA_DIR")
		if dataDir == "" {
			root := getenv("CLINE_DIR")
			if root == "" {
				homeKey := "HOME"
				if runtime.GOOS == "windows" {
					homeKey = "USERPROFILE"
				}
				home := getenv(homeKey)
				if home == "" {
					var err error
					home, err = os.UserHomeDir()
					if err != nil {
						return "", fmt.Errorf("cline: resolve provider settings home: %w", err)
					}
				}
				root = filepath.Join(home, ".cline")
			}
			dataDir = filepath.Join(root, "data")
		}
		path = filepath.Join(dataDir, "settings", "providers.json")
	}
	if !filepath.IsAbs(path) && workspacePath != "" {
		path = filepath.Join(workspacePath, path)
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cline: read provider selection: %w", err)
	}
	var file clineProvidersFile
	if err := json.Unmarshal(data, &file); err != nil {
		return "", fmt.Errorf("cline: decode provider selection; reconfigure with cline auth: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	selected := strings.TrimSpace(file.LastUsedProvider)
	if selected == "" {
		return "", nil // No saved choice: leave Cline's own fallback in charge.
	}
	if _, ok := file.Providers[selected]; !ok {
		return "", fmt.Errorf("cline: saved provider %q is missing; reconfigure with cline auth", selected)
	}
	return selected, nil
}
