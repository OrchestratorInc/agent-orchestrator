package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Claude Pro/Max plans are not a native OpenCode sign-in in either major
// version: OpenCode's built-in Anthropic provider only accepts an API key. The
// community opencode-claude-auth plugin adds a "Claude Code subscription"
// method that imports the user's own Claude Code login. AO pins the last
// release for each plugin API: 2.2.1 is the final OpenCode 1 build and 3.x
// targets OpenCode 2.
const (
	ClaudePlanPluginV1 = "opencode-claude-auth@2.2.1"
	ClaudePlanPluginV2 = "opencode-claude-auth@3.0.0"
	// ClaudePlanMethodV2 is the plugin's OpenCode 2 auth method ID.
	ClaudePlanMethodV2 = "claude-code"
)

// ClaudePlanPlugin returns the pinned plugin for an OpenCode major version.
func ClaudePlanPlugin(major int) string {
	if major >= 2 {
		return ClaudePlanPluginV2
	}
	return ClaudePlanPluginV1
}

// ClaudePlanPluginKey is the config key each major version loads plugins from.
func ClaudePlanPluginKey(major int) string {
	if major >= 2 {
		return "plugins"
	}
	return "plugin"
}

// claudePlanAuthPluginMarkers identify an Anthropic subscription plugin the user
// already loads themselves; AO then adds nothing, so two plugins never compete
// for the same credential.
var claudePlanAuthPluginMarkers = []string{"opencode-claude-auth", "opencode-anthropic-auth"}

// ClaudePlanPlugins returns the plugin list AO adds to a session of the given
// major version: the pinned plugin when the stored Anthropic credential is a
// subscription (OAuth) login, and nothing otherwise. dataHome is the
// XDG_DATA_HOME the session runs with; empty means the user's own data home.
// Detection only reads local files and never fails a launch.
func ClaudePlanPlugins(ctx context.Context, major int, dataHome string) []string {
	if ctx.Err() != nil || userConfigLoadsClaudeAuthPlugin() {
		return nil
	}
	dataDir := ""
	if strings.TrimSpace(dataHome) != "" {
		dataDir = filepath.Join(dataHome, "opencode")
	} else if dir, ok := opencodeDataDir(); ok {
		dataDir = dir
	}
	if dataDir == "" || !claudePlanCredentialStored(ctx, dataDir) {
		return nil
	}
	return []string{ClaudePlanPlugin(major)}
}

// claudePlanCredentialStored reports an Anthropic OAuth credential in either of
// OpenCode's stores: auth.json (OpenCode 1) or the credential table (OpenCode 2).
// OpenCode's own Anthropic provider has no OAuth method, so an OAuth record
// here can only come from a subscription plugin.
func claudePlanCredentialStored(ctx context.Context, dataDir string) bool {
	if data, err := os.ReadFile(filepath.Join(dataDir, "auth.json")); err == nil {
		var entries map[string]struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &entries) == nil && strings.EqualFold(entries["anthropic"].Type, "oauth") {
			return true
		}
	}
	path := filepath.Join(dataDir, "opencode.db")
	if _, err := os.Stat(path); err != nil {
		return false
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return false
	}
	defer func() { _ = db.Close() }()
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM credential WHERE active = 1 AND integration_id = 'anthropic' AND json_extract(value, '$.type') = 'oauth'`).Scan(&count)
	return err == nil && count > 0
}

// userConfigLoadsClaudeAuthPlugin reports whether the user's global OpenCode
// config (or their OPENCODE_CONFIG file) already names a subscription plugin.
func userConfigLoadsClaudeAuthPlugin() bool {
	var paths []string
	if custom := strings.TrimSpace(os.Getenv(opencodeConfigEnvVar)); custom != "" {
		paths = append(paths, custom)
	}
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			configHome = filepath.Join(home, ".config")
		}
	}
	if configHome != "" {
		for _, name := range []string{"opencode.json", "opencode.jsonc", "config.json"} {
			paths = append(paths, filepath.Join(configHome, "opencode", name))
		}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, marker := range claudePlanAuthPluginMarkers {
			if strings.Contains(string(data), marker) {
				return true
			}
		}
	}
	return false
}

// AddConfigPlugins merges plugins into the list under key in an inline OpenCode
// config object, keeping every entry already there. Empty plugins returns
// content unchanged.
func AddConfigPlugins(content, key string, plugins []string) (string, error) {
	if len(plugins) == 0 {
		return content, nil
	}
	config := map[string]json.RawMessage{}
	if strings.TrimSpace(content) != "" {
		if err := json.Unmarshal([]byte(content), &config); err != nil || config == nil {
			return "", fmt.Errorf("opencode: OPENCODE_CONFIG_CONTENT must be a JSON object")
		}
	}
	var list []string
	if raw, ok := config[key]; ok && strings.TrimSpace(string(raw)) != "null" {
		if err := json.Unmarshal(raw, &list); err != nil {
			return "", fmt.Errorf("opencode: OPENCODE_CONFIG_CONTENT %s must be a list of strings", key)
		}
	}
	list = mergePlugins(list, plugins)
	raw, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	config[key] = raw
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func mergePlugins(existing, add []string) []string {
	out := append([]string(nil), existing...)
	for _, plugin := range add {
		present := false
		for _, have := range out {
			if have == plugin {
				present = true
				break
			}
		}
		if !present {
			out = append(out, plugin)
		}
	}
	return out
}
