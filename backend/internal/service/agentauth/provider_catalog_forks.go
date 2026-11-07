package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// xdgDir returns $env/name when env holds an absolute path, else
// ~/fallback/name.
func xdgDir(env, fallback, name string) (string, error) {
	if dir := strings.TrimSpace(os.Getenv(env)); filepath.IsAbs(dir) {
		return filepath.Join(dir, name), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("home directory is unavailable")
	}
	return filepath.Join(home, fallback, name), nil
}

// Kilo Code is an OpenCode fork: the same models.dev catalog and auth.json.

func kiloCatalogPath() (string, error) {
	dir, err := xdgDir("XDG_CACHE_HOME", ".cache", "kilo")
	return filepath.Join(dir, "models.json"), err
}

func saveKiloProviderKey(ctx context.Context, _ catalogHarness, providerID, key string) error {
	dir := strings.TrimSpace(os.Getenv("KILO_DATA_DIR"))
	if dir == "" {
		var err error
		if dir, err = xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share"), "kilo"); err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Kilo Code's credential home is unavailable.")
		}
	}
	return saveModelsDevAuth(ctx, dir, "Kilo Code", providerID, key)
}

// MiMo Code is an OpenCode fork too. Its data directory comes from its own
// `debug paths`, which AO's MiMo adapter already reads to check sign-in.

func mimoCatalogPath() (string, error) {
	dir, err := xdgDir("XDG_CACHE_HOME", ".cache", "mimocode")
	return filepath.Join(dir, "models.json"), err
}

func saveMiMoProviderKey(ctx context.Context, harness catalogHarness, providerID, key string) error {
	if harness.binary == "" || harness.command == nil {
		return apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "mimo was not found on PATH.")
	}
	pathsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := harness.command(pathsCtx, harness.binary, "debug", "paths")
	process.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 3 * time.Second
	cmd.Dir = harness.workDir
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	output, err := cmd.Output()
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "MiMo Code did not report its data directory.")
	}
	for _, line := range strings.Split(string(output), "\n") {
		if name, dir, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == "data" {
			return saveModelsDevAuth(ctx, strings.TrimSpace(dir), "MiMo Code", providerID, key)
		}
	}
	return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "MiMo Code did not report its data directory.")
}

// Crush caches Charm's Catwalk provider catalog in its data directory and
// keeps keys entered in its UI in the crush.json beside it, under
// providers.<id>.api_key.

func crushDataDir() (string, error) {
	if runtime.GOOS == "windows" {
		return xdgDir("LOCALAPPDATA", filepath.Join("AppData", "Local"), "crush")
	}
	return xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share"), "crush")
}

func crushCatalogPath() (string, error) {
	dir, err := crushDataDir()
	return filepath.Join(dir, "providers.json"), err
}

// readCatwalkCatalog keeps providers whose key comes from one variable
// ("$NAME"). Providers with no key (cloud credentials) or that also need an
// endpoint (Azure) stay with Crush's own setup.
func readCatwalkCatalog(data []byte) ([]catalogEntry, error) {
	var raw []struct {
		ID     string            `json:"id"`
		Name   string            `json:"name"`
		APIKey string            `json:"api_key"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]catalogEntry, 0, len(raw))
	for _, provider := range raw {
		env := strings.TrimPrefix(provider.APIKey, "$")
		if !catalogProviderID.MatchString(provider.ID) || provider.ID == "azure" || !strings.HasPrefix(provider.APIKey, "$") || !catalogKeyVariable.MatchString(env) {
			continue
		}
		name := strings.TrimSpace(provider.Name)
		if name == "" || len(name) > 128 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			name = provider.ID
		}
		out = append(out, catalogEntry{CatalogProvider: CatalogProvider{ID: provider.ID, Name: name, Env: env, Models: len(provider.Models)}})
	}
	return out, nil
}

func saveCrushProviderKey(ctx context.Context, _ catalogHarness, providerID, key string) error {
	// Crush expands $NAME and $(command) in config values.
	if strings.Contains(key, "$") {
		return apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Crush reads a key containing $ as a variable or command. Enter the key itself.", nil)
	}
	dir, err := crushDataDir()
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's data directory is unavailable.")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's data directory could not be prepared.")
	}
	path := filepath.Join(dir, "crush.json")
	config := map[string]json.RawMessage{}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 4*1024*1024 {
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Crush's data file is not a safe regular file.", nil)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's data file could not be read.")
		}
		if strings.TrimSpace(string(data)) != "" && (json.Unmarshal(data, &config) != nil || config == nil) {
			return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Crush's data file is invalid; use Crush to repair it.", nil)
		}
	} else if !os.IsNotExist(err) {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's data file could not be inspected.")
	}
	providers := map[string]map[string]json.RawMessage{}
	if raw, ok := config["providers"]; ok && json.Unmarshal(raw, &providers) != nil {
		return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Crush's provider settings are invalid; use Crush to repair them.", nil)
	}
	if providers == nil {
		providers = map[string]map[string]json.RawMessage{}
	}
	entry := providers[providerID]
	if entry == nil {
		entry = map[string]json.RawMessage{}
	}
	entry["api_key"], _ = json.Marshal(key)
	providers[providerID] = entry
	config["providers"], _ = json.Marshal(providers)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return apierr.Internal("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's settings could not be encoded.")
	}
	return replaceCredentialFile(ctx, dir, path, append(data, '\n'), "Crush")
}
