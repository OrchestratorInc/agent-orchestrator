package modelcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// These are native catalog snapshots, not credentials. Supplement auth-filtered
// CLI output without making authenticated requests or inventing provider names.
// Missing/unsupported snapshots leave the CLI result intact.
const providerCatalogReadLimit = 32 << 20

// hasLocalProviderCatalog reports whether localProviderCatalog reads a native
// provider cache for agentID.
func hasLocalProviderCatalog(agentID string) bool {
	switch agentID {
	case "opencode", "kilocode", "mimo-code", "omp", "pi", "kimchi", "prime-agent":
		return true
	}
	return false
}

// listsRunnableModels reports an agent whose model command lists only the
// models it can run now, while its provider cache names every model it knows.
func listsRunnableModels(agentID string) bool {
	switch agentID {
	case "opencode", "kilocode", "mimo-code":
		return true
	}
	return false
}

func localProviderCatalog(ctx context.Context, agentID, binary, workingDir string, env map[string]string) ports.AgentModelCatalog {
	var result ports.AgentModelCatalog
	switch agentID {
	case "opencode", "kilocode", "mimo-code":
		if raw, err := readProviderCatalog(providerCachePath(agentID, env)); err == nil {
			result = parseProviderSnapshot(raw)
		}
	case "omp":
		result = ompProviderCatalog(ctx, filepath.Join(piCatalogDir(agentID, env), "models.db"))
	case "pi", "kimchi":
		dir := piCatalogDir(agentID, env)
		if raw, err := readProviderCatalog(filepath.Join(dir, "models-store.json")); err == nil {
			result = parseProviderSnapshot(raw)
		}
		// User-declared providers remain visible even before their first login.
		if raw, err := readProviderCatalog(filepath.Join(dir, "models.json")); err == nil {
			var config struct {
				Providers json.RawMessage `json:"providers"`
			}
			if json.Unmarshal(raw, &config) == nil {
				custom := parseProviderSnapshot(config.Providers)
				result.Models = append(result.Models, custom.Models...)
				result.Providers = append(result.Providers, custom.Providers...)
			}
		}
		if agentID == "pi" {
			result.Models = append(result.Models, piInstalledModels(ctx, binary, workingDir, env)...)
		} else {
			// Kimchi's chat has no thinking control.
			for i := range result.Models {
				result.Models[i].Efforts = nil
			}
		}
	case "prime-agent":
		// Prime Agent keeps no provider cache; its installed pi-ai fork lists
		// every built-in provider's models.
		result.Models = piInstalledModels(ctx, binary, workingDir, env)
	}
	return result
}

func providerCachePath(agentID string, env map[string]string) string {
	name := map[string]string{"opencode": "opencode", "kilocode": "kilo", "mimo-code": "mimocode"}[agentID]
	if name == "" {
		return ""
	}
	root := envValue(env, "XDG_CACHE_HOME")
	if root == "" {
		root = filepath.Join(catalogHome(env), ".cache")
	}
	return filepath.Join(root, name, "models.json")
}

func catalogHome(env map[string]string) string {
	if home := envValue(env, "HOME"); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return home
}

func piCatalogDir(agentID string, env map[string]string) string {
	// Kimchi runs its embedded Pi runtime from its own harness directory and
	// overrides PI_CODING_AGENT_DIR to point there, so Pi's directory and that
	// variable say nothing about Kimchi's providers.
	if agentID == "kimchi" {
		return filepath.Join(catalogHome(env), ".config", "kimchi", "harness")
	}
	if dir := envValue(env, "PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(catalogHome(env), "."+agentID, "agent")
}

func readProviderCatalog(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, providerCatalogReadLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > providerCatalogReadLimit {
		return nil, io.ErrShortBuffer
	}
	return raw, nil
}

// Both models.dev caches and Pi's model store key their records by provider.
// Decode only model metadata; apiKey, headers, URLs and other config stay local.
func parseProviderSnapshot(raw []byte) ports.AgentModelCatalog {
	var providers map[string]struct {
		Models json.RawMessage `json:"models"`
	}
	var result ports.AgentModelCatalog
	if json.Unmarshal(raw, &providers) != nil {
		return result
	}
	for provider, entry := range providers {
		if strings.TrimSpace(provider) == "" {
			continue
		}
		result.Providers = append(result.Providers, provider)
		result.Models = append(result.Models, parseProviderModelRecords(entry.Models, provider)...)
	}
	sort.Strings(result.Providers)
	return result
}

func parseProviderModelRecords(raw []byte, provider string) []ports.AgentModelInfo {
	var records []map[string]any
	if json.Unmarshal(raw, &records) != nil {
		var keyed map[string]map[string]any
		if json.Unmarshal(raw, &keyed) != nil {
			return nil
		}
		records = make([]map[string]any, 0, len(keyed))
		for id, record := range keyed {
			if record == nil {
				continue
			}
			if firstString(record, "id") == "" {
				record["id"] = id
			}
			records = append(records, record)
		}
	}
	models := make([]ports.AgentModelInfo, 0, len(records))
	for _, record := range records {
		id := firstString(record, "id")
		if id == "" {
			continue
		}
		modelProvider := provider
		if modelProvider == "" {
			modelProvider = firstString(record, "provider")
		}
		if modelProvider == "" {
			continue
		}
		label := firstString(record, "name", "label")
		if label == "" {
			label = id
		}
		// These native catalogs store unqualified model IDs, including org/model.
		models = append(models, ports.AgentModelInfo{ID: modelProvider + "/" + id, Label: label, Provider: modelProvider,
			ContextWindow: catalogContextWindow(record), Inputs: catalogInputs(record), Efforts: piThinkingLevels(record)})
	}
	return models
}

// piThinkingLevels are a reasoning Pi model's thinking levels as effort choices,
// so a switch to Pi can pick one before Pi runs. thinkingLevelMap sets a level
// the model lacks to null; only levels pi-acp's bundled runtime takes are
// offered. Records without the map (OpenCode's cache) name none.
func piThinkingLevels(record map[string]any) []string {
	levels, mapped := record["thinkingLevelMap"].(map[string]any)
	if reasoning, _ := record["reasoning"].(bool); !reasoning || !mapped {
		return nil
	}
	var efforts []string
	for _, level := range []string{"minimal", "low", "medium", "high", "xhigh"} {
		if value, named := levels[level]; !named || value != nil {
			efforts = append(efforts, level)
		}
	}
	return efforts
}

func ompProviderCatalog(ctx context.Context, path string) ports.AgentModelCatalog {
	var result ports.AgentModelCatalog
	if _, err := os.Stat(path); err != nil {
		return result
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return result
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT models FROM model_cache WHERE length(models) <= ?`, providerCatalogReadLimit)
	if err != nil {
		return result
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			return ports.AgentModelCatalog{}
		}
		// Cache keys can include account-specific suffixes: trust model.provider,
		// never expose the cache key as a provider or infer it from a selector.
		result.Models = append(result.Models, parseProviderModelRecords(raw, "")...)
	}
	if rows.Err() != nil {
		return ports.AgentModelCatalog{}
	}
	return result
}

// Resolve Pi's own installed dependency, never a package from the repository or
// another global install. Only the static generated catalog is imported: no
// auth storage, extensions, session creation, or provider request is involved.
const piInstalledCatalogScript = `
const {createRequire} = await import('node:module');
const {pathToFileURL} = await import('node:url');
const {dirname, join} = await import('node:path');
const {readFileSync, existsSync} = await import('node:fs');
let dir = dirname(process.argv[1]);
while (!existsSync(join(dir, 'package.json')) && dirname(dir) !== dir) dir = dirname(dir);
const owner = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'));
const owners = new Set(['@earendil-works/pi-coding-agent', '@mariozechner/pi-coding-agent', 'prime-agent']);
const scope = !owners.has(owner.name) ? undefined
 : owner.dependencies?.['@earendil-works/pi-ai'] ? '@earendil-works'
 : owner.dependencies?.['@mariozechner/pi-ai'] ? '@mariozechner' : undefined;
if (!scope) process.exit(1);
const req = createRequire(pathToFileURL(process.argv[1]));
let entry;
try {
 entry = req.resolve(scope + '/pi-ai');
} catch {
 // Newer pi-ai exports only an "import" condition, which require-style
 // resolution rejects; look the package up the way Node would place it.
 for (let at = dir; ; at = dirname(at)) {
  const candidate = join(at, 'node_modules', scope, 'pi-ai', 'dist', 'index.js');
  if (existsSync(candidate)) { entry = candidate; break; }
  if (dirname(at) === at) process.exit(1);
 }
}
const pkg = JSON.parse(readFileSync(join(dirname(entry), '..', 'package.json'), 'utf8'));
// Prime Agent ships its pi-ai fork as "prime-agent-ai" under the pi-ai name.
if (pkg.name !== scope + '/pi-ai' && !(owner.name === 'prime-agent' && pkg.name === 'prime-agent-ai')) process.exit(1);
const {MODELS} = await import(pathToFileURL(join(dirname(entry), 'models.generated.js')).href);
const models = Object.values(MODELS).flatMap(group => Object.values(group)).map(m =>
 ({id: m.id, name: m.name, provider: m.provider, contextWindow: m.contextWindow, input: m.input}));
process.stdout.write(JSON.stringify(models));
`

func piInstalledModels(ctx context.Context, binary, workingDir string, env map[string]string) []ports.AgentModelInfo {
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil || !filepath.IsAbs(resolved) {
		return nil
	}
	// A compiled standalone Pi has no resolvable JS package. Keep its CLI/cache
	// results rather than borrowing a different installation's catalog.
	if !strings.HasSuffix(resolved, ".js") && !strings.HasSuffix(resolved, ".mjs") {
		return nil
	}
	output, err := modelCommand(ctx, "node", []string{"--input-type=module", "-e", piInstalledCatalogScript, resolved}, workingDir, env).Output()
	if err != nil {
		return nil
	}
	return parseProviderModelRecords(output, "")
}

func providerCatalogFingerprint(agentID string, env map[string]string) string {
	var paths []string
	switch agentID {
	case "opencode", "kilocode", "mimo-code":
		paths = []string{providerCachePath(agentID, env)}
	case "pi", "kimchi":
		dir := piCatalogDir(agentID, env)
		paths = []string{filepath.Join(dir, "models-store.json"), filepath.Join(dir, "models.json")}
	case "omp":
		path := filepath.Join(piCatalogDir(agentID, env), "models.db")
		paths = []string{path, path + "-wal"}
	}
	if agentID == "pi" || agentID == "kimchi" {
		// Pi and Kimchi rewrite these files with the same contents on every
		// --list-models, so a modification time would invalidate every discovery.
		return ";" + fingerprintConfigPaths(paths)
	}
	var stamp strings.Builder
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			_, _ = fmt.Fprintf(&stamp, ";%s:%d:%d", path, info.Size(), info.ModTime().UnixNano())
		}
	}
	return stamp.String()
}

// Preserve explicit JSON provider containers even when their model array is empty.
func reportedJSONProviders(raw []byte) []string {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	found := map[string]bool{}
	var walk func(any, bool)
	walk = func(value any, inProviders bool) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child, inProviders)
			}
		case map[string]any:
			_, hasModels := node["models"]
			if hasModels || inProviders {
				if id := firstString(node, "provider", "providerId", "provider_id", "id"); id != "" {
					found[id] = true
				}
			}
			for key, child := range node {
				walk(child, key == "providers")
			}
		}
	}
	walk(root, false)
	providers := make([]string, 0, len(found))
	for id := range found {
		providers = append(providers, id)
	}
	sort.Strings(providers)
	return providers
}
