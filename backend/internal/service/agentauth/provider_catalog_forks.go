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

// crushDataDir mirrors Crush's GlobalConfigData: CRUSH_GLOBAL_DATA, then
// XDG_DATA_HOME (on Windows too), then %LOCALAPPDATA% or ~/.local/share.
func crushDataDir() (string, error) {
	var dir string
	switch {
	case os.Getenv("CRUSH_GLOBAL_DATA") != "":
		dir = os.Getenv("CRUSH_GLOBAL_DATA")
	case os.Getenv("XDG_DATA_HOME") != "":
		dir = filepath.Join(os.Getenv("XDG_DATA_HOME"), "crush")
	case runtime.GOOS == "windows":
		return xdgDir("LOCALAPPDATA", filepath.Join("AppData", "Local"), "crush")
	default:
		return xdgDir("", filepath.Join(".local", "share"), "crush")
	}
	// A relative directory would resolve against each process's own working
	// directory, which differs between AO and Crush.
	if !filepath.IsAbs(dir) {
		return "", errors.New("crush data directory is not absolute")
	}
	return dir, nil
}

// crushLockWait matches Crush's own wait for its configuration lock.
const crushLockWait = 5 * time.Second

// lockCrushConfig takes the lock Crush holds on "<file>.lock" while it
// rewrites file, so a token refresh or model list Crush saves concurrently
// is never lost.
func lockCrushConfig(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // Crush's lock beside its data file
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's data file could not be locked.")
	}
	deadline := time.Now().Add(crushLockWait)
	for {
		release, busy, err := tryLockFile(file)
		if err == nil {
			return func() {
				release()
				_ = file.Close()
			}, nil
		}
		if !busy {
			_ = file.Close()
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's data file could not be locked.")
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, apierr.Conflict("AGENT_AUTH_STORE_BUSY", "Crush is saving its settings. Try again in a moment.", nil)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// crushSignInFields are what Crush itself removes when a key is saved for a
// provider, so a stored sign-in or its model list never replaces the key.
// Crush drops all of providers.anthropic while a Claude sign-in is stored.
var crushSignInFields = map[string][]string{
	"openai":    {"oauth", "chatgpt_models"},
	"xai":       {"oauth", "grok_models"},
	"anthropic": {"oauth"},
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
		models := make([]string, 0, len(provider.Models))
		for _, raw := range provider.Models {
			var model struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &model) == nil && model.ID != "" {
				models = append(models, model.ID)
			}
		}
		out = append(out, catalogEntry{CatalogProvider: CatalogProvider{ID: provider.ID, Name: name, Env: env, Models: len(models)}, models: models})
	}
	return out, nil
}

func saveCrushProviderKey(ctx context.Context, _ catalogHarness, providerID, key string) error {
	// Crush passes a value holding any of these through its shell expander,
	// which also runs backtick commands and rewrites backslash escapes.
	if strings.ContainsAny(key, "$`\\'\"") {
		return apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Crush reads $, backticks, quotes and backslashes in a key as shell syntax. Enter a key without them.", nil)
	}
	dir, err := crushDataDir()
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's data directory is unavailable.")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's data directory could not be prepared.")
	}
	path := filepath.Join(dir, "crush.json")
	unlock, err := lockCrushConfig(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
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
	for _, field := range crushSignInFields[providerID] {
		delete(entry, field)
	}
	providers[providerID] = entry
	config["providers"], _ = json.Marshal(providers)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return apierr.Internal("AGENT_AUTH_STORE_UNAVAILABLE", "Crush's settings could not be encoded.")
	}
	return replaceCredentialFile(ctx, dir, path, append(data, '\n'), "Crush")
}
