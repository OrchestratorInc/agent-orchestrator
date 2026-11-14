package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const catalogKeyMethod = "provider_key"

type CatalogProvider struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Env    string `json:"env,omitempty"`
	Models int    `json:"models"`
}

type catalogEntry struct {
	CatalogProvider
	models []string
}

type providerCatalog struct {
	path   func() (string, error)
	read   func([]byte) ([]catalogEntry, error)
	static []CatalogProvider
	save   func(ctx context.Context, harness catalogHarness, providerID, key string) error
}

type catalogHarness struct {
	binary  string
	workDir string
	command func(context.Context, string, ...string) *exec.Cmd
}

var providerCatalogs = map[string]providerCatalog{
	"opencode": {path: openCodeCatalogPath, read: readModelsDevCatalog, save: func(ctx context.Context, _ catalogHarness, providerID, key string) error {
		return saveOpenCodeProviderKey(ctx, providerID, key)
	}},
	"pi": {static: piCatalog, save: func(ctx context.Context, _ catalogHarness, providerID, key string) error {
		return savePiProviderKey(ctx, providerID, key)
	}},
	"omp":       {static: ompCatalog, save: saveOMPProviderKey},
	"kilocode":  {path: kiloCatalogPath, read: readModelsDevCatalog, save: saveKiloProviderKey},
	"mimo-code": {path: mimoCatalogPath, read: readModelsDevCatalog, save: saveMiMoProviderKey},
	"crush":     {path: crushCatalogPath, read: readCatwalkCatalog, save: saveCrushProviderKey},
}

var (
	catalogProviderID  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	catalogKeyVariable = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(_API_KEY|_APIKEY|_KEY|_TOKEN)$`)
)

const maxCatalogBytes = 64 << 20

type catalogCacheEntry struct {
	modTime time.Time
	size    int64
	entries []catalogEntry
}

var (
	catalogCacheMu sync.Mutex
	catalogCache   = map[string]catalogCacheEntry{}
)

func catalogProviders(agentID string) ([]catalogEntry, bool) {
	catalog, ok := providerCatalogs[agentID]
	if !ok {
		return nil, false
	}
	if catalog.static != nil {
		entries := make([]catalogEntry, len(catalog.static))
		for i, provider := range catalog.static {
			entries[i] = catalogEntry{CatalogProvider: provider}
		}
		return entries, len(entries) > 0
	}
	path, err := catalog.path()
	if err != nil {
		return nil, false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCatalogBytes {
		return nil, false
	}
	catalogCacheMu.Lock()
	defer catalogCacheMu.Unlock()
	if cached, ok := catalogCache[path]; ok && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.entries, len(cached.entries) > 0
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCatalogBytes+1))
	_ = file.Close()
	if err != nil || len(data) > maxCatalogBytes {
		return nil, false
	}
	entries, err := catalog.read(data)
	if err != nil {
		return nil, false
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	catalogCache[path] = catalogCacheEntry{modTime: info.ModTime(), size: info.Size(), entries: entries}
	return entries, len(entries) > 0
}

func readModelsDevCatalog(data []byte) ([]catalogEntry, error) {
	var raw map[string]struct {
		ID     string                     `json:"id"`
		Name   string                     `json:"name"`
		Env    []string                   `json:"env"`
		Models map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]catalogEntry, 0, len(raw))
	for id, provider := range raw {
		if !catalogProviderID.MatchString(id) || len(provider.Env) == 0 {
			continue
		}
		keyOnly := true
		for _, name := range provider.Env {
			if !catalogKeyVariable.MatchString(name) {
				keyOnly = false
				break
			}
		}
		if !keyOnly {
			continue
		}
		name := strings.TrimSpace(provider.Name)
		if name == "" || len(name) > 128 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			name = id
		}
		models := make([]string, 0, len(provider.Models))
		for model := range provider.Models {
			if providerModelID.MatchString(model) {
				models = append(models, model)
			}
		}
		sort.Strings(models)
		out = append(out, catalogEntry{CatalogProvider: CatalogProvider{ID: id, Name: name, Env: provider.Env[0], Models: len(models)}, models: models})
	}
	return out, nil
}

func openCodeCatalogPath() (string, error) {
	cache := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME"))
	if !filepath.IsAbs(cache) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("home directory is unavailable")
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "opencode", "models.json"), nil
}

func saveOpenCodeProviderKey(ctx context.Context, providerID, key string) error {
	return saveOpenCodeAPIKey(ctx, providerID+"_key", key)
}

func (s *Service) CatalogProviders(_ context.Context, agentID string) ([]CatalogProvider, error) {
	if _, ok := planByAgentID[agentID]; !ok {
		return nil, apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", "Unknown agent authentication target.", nil)
	}
	entries, ok := catalogProviders(agentID)
	if !ok {
		return nil, apierr.NotFound("AGENT_AUTH_CATALOG_UNAVAILABLE", "This harness has no provider catalog on this device. Run it once, or use its native sign-in.")
	}
	out := make([]CatalogProvider, len(entries))
	for i, entry := range entries {
		out[i] = entry.CatalogProvider
	}
	return out, nil
}

func (s *Service) CatalogModels(_ context.Context, agentID, providerID string) ([]string, error) {
	entry, err := catalogProvider(agentID, providerID)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), entry.models...), nil
}

func catalogProvider(agentID, providerID string) (catalogEntry, error) {
	entries, ok := catalogProviders(agentID)
	if !ok {
		return catalogEntry{}, apierr.NotFound("AGENT_AUTH_CATALOG_UNAVAILABLE", "This harness has no provider catalog on this device.")
	}
	for _, entry := range entries {
		if entry.ID == providerID {
			return entry, nil
		}
	}
	return catalogEntry{}, apierr.Invalid("AGENT_AUTH_PROVIDER_UNKNOWN", "Choose a provider from this harness's catalog.", nil)
}

func (s *Service) saveCatalogKey(ctx context.Context, plan Plan, providerID, key string) error {
	agentID := plan.AgentID
	if _, err := catalogProvider(agentID, providerID); err != nil {
		return err
	}
	harness := catalogHarness{command: s.commandContext}
	if len(plan.command) > 0 {
		harness.binary = plan.command[0]
	}
	if s.dataDir != "" {
		harness.workDir = filepath.Join(s.dataDir, authWorkspaceRootName, "ui-"+agentID)
		if err := os.MkdirAll(harness.workDir, 0o700); err != nil {
			return apierr.Unavailable("AGENT_AUTH_WORKSPACE_UNAVAILABLE", "The login workspace could not be prepared.")
		}
	}
	return providerCatalogs[agentID].save(ctx, harness, providerID, key)
}
