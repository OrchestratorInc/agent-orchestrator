package modelcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // read OMP's native credential store
)

// configuredProviders is advisory evidence, never an auth verdict or a filter.
// Missing/unsupported stores and unresolved key commands leave status unknown.
func configuredProviders(ctx context.Context, agentID string, env map[string]string) []string {
	if agentID != "opencode" && agentID != "opencode-v2" && agentID != "kilocode" && agentID != "mimo-code" && agentID != "pi" && agentID != "omp" {
		return nil
	}
	value := func(name string) string {
		if v, ok := env[name]; ok {
			return credentialPresence(v)
		}
		return credentialPresence(os.Getenv(name))
	}
	found := map[string]bool{}
	if agentID == "opencode" || agentID == "opencode-v2" {
		if value("OPENCODE_API_KEY") != "" {
			found["opencode"] = true
		}
		if value("COHERE_API_KEY") != "" {
			found["cohere"] = true
		}
	}
	// These native provider identifiers are shared by Pi and OpenCode. Do not
	// infer an environment mapping for arbitrary/custom provider names.
	if agentID != "omp" {
		for provider, names := range map[string][]string{
			"anthropic": {"ANTHROPIC_API_KEY"}, "openai": {"OPENAI_API_KEY"},
			"google": {"GEMINI_API_KEY", "GOOGLE_API_KEY"}, "groq": {"GROQ_API_KEY"},
			"xai": {"XAI_API_KEY"}, "openrouter": {"OPENROUTER_API_KEY"},
			"mistral": {"MISTRAL_API_KEY"}, "deepseek": {"DEEPSEEK_API_KEY"},
		} {
			for _, name := range names {
				if value(name) != "" {
					found[provider] = true
				}
			}
		}
	}
	home := value("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	var authPath string
	switch agentID {
	case "pi", "omp":
		dir := value("PI_CODING_AGENT_DIR")
		if dir == "" && home != "" {
			dir = filepath.Join(home, "."+agentID, "agent")
		}
		if dir != "" {
			// An existing OMP database is authoritative, including disabled rows.
			// Falling back to legacy JSON then could revive removed credentials.
			if agentID == "omp" {
				if providers, ok := ompConfiguredProviders(ctx, filepath.Join(dir, "agent.db")); ok {
					return providers
				}
			}
			authPath = filepath.Join(dir, "auth.json")
		}
	case "opencode", "kilocode", "mimo-code":
		name := map[string]string{"opencode": "opencode", "kilocode": "kilo", "mimo-code": "mimocode"}[agentID]
		dir := ""
		if agentID == "opencode" {
			dir = value("OPENCODE_DATA_DIR")
		}
		if dir == "" {
			dataHome := value("XDG_DATA_HOME")
			if dataHome == "" && home != "" {
				dataHome = filepath.Join(home, ".local", "share")
			}
			if dataHome != "" {
				dir = filepath.Join(dataHome, name)
			}
		}
		if dir != "" {
			authPath = filepath.Join(dir, "auth.json")
		}
		// OpenCode 2 uses integrations in its native database. Legacy auth.json
		// isn't authoritative there; only known environment evidence is reported.
	}
	if authPath != "" {
		if raw, err := readModelConfig(authPath); err == nil {
			var entries map[string]struct {
				Type    string `json:"type"`
				Key     string `json:"key"`
				Access  string `json:"access"`
				Refresh string `json:"refresh"`
			}
			if json.Unmarshal(raw, &entries) == nil {
				for provider, entry := range entries {
					if strings.TrimSpace(provider) == "" {
						continue
					}
					if entry.Type == "oauth" && (strings.TrimSpace(entry.Access) != "" || strings.TrimSpace(entry.Refresh) != "") {
						found[provider] = true
					}
					if entry.Type != "api" && entry.Type != "api_key" {
						continue
					}
					key := strings.TrimSpace(entry.Key)
					if agentID == "pi" || agentID == "omp" {
						if strings.HasPrefix(key, "!") {
							continue
						}
						resolved := true
						key = os.Expand(key, func(name string) string {
							if name == "$" || name == "!" {
								return name
							}
							v := value(name)
							if v == "" {
								resolved = false
							}
							return v
						})
						if !resolved {
							continue
						}
					}
					if strings.TrimSpace(key) != "" {
						found[provider] = true
					}
				}
			}
		}
	}
	// The native models.dev catalog declares each provider's credential variables.
	// This covers providers beyond AO's small fallback map without guessing names.
	for provider, names := range providerEnvNames(providerCachePath(agentID, env)) {
		for _, key := range names {
			if key != "" && value(key) != "" {
				found[provider] = true
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

func ompConfiguredProviders(ctx context.Context, path string) ([]string, bool) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, false
	} else if err != nil {
		return nil, true
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, true
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT provider FROM auth_credentials
		WHERE disabled_cause IS NULL AND trim(coalesce(data, '')) <> '' AND trim(provider) <> '' ORDER BY provider`)
	if err != nil {
		return nil, true
	}
	defer func() { _ = rows.Close() }()
	var providers []string
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return nil, true
		}
		providers = append(providers, provider)
	}
	if rows.Err() != nil {
		return nil, true
	}
	return providers, true
}

// Catalog-only cloud placeholders never count as configured credentials.
func credentialPresence(value string) string {
	value = strings.TrimSpace(value)
	if value == modelDiscoveryPresenceValue {
		return ""
	}
	return value
}

type providerEnvEntry struct {
	size    int64
	modTime time.Time
	names   map[string][]string
}

var providerEnvCache struct {
	sync.Mutex
	entries map[string]providerEnvEntry
}

// providerEnvNames returns the credential variables a models.dev cache
// declares for each provider. The cache can be tens of megabytes and catalog
// fingerprints consult it on every cached read, so a parse is kept until the
// file's size or modification time changes.
func providerEnvNames(path string) map[string][]string {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	providerEnvCache.Lock()
	cached, ok := providerEnvCache.entries[path]
	providerEnvCache.Unlock()
	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.names
	}
	raw, err := readProviderCatalog(path)
	if err != nil {
		return nil
	}
	var entries map[string]struct {
		Env []string `json:"env"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	names := make(map[string][]string, len(entries))
	for provider, entry := range entries {
		if len(entry.Env) > 0 {
			names[provider] = entry.Env
		}
	}
	providerEnvCache.Lock()
	if providerEnvCache.entries == nil {
		providerEnvCache.entries = make(map[string]providerEnvEntry)
	}
	providerEnvCache.entries[path] = providerEnvEntry{size: info.Size(), modTime: info.ModTime(), names: names}
	providerEnvCache.Unlock()
	return names
}
