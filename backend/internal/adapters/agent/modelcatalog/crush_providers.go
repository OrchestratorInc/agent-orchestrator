package modelcatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func init() { browseProviderCredentials["crush"] = crushConfiguredProviders }

// crushProviderKeyEnv is the key variable of Crush's bundled provider list, for
// when its cached providers.json is missing.
var crushProviderKeyEnv = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY", "openai": "OPENAI_API_KEY", "gemini": "GEMINI_API_KEY",
	"xai": "XAI_API_KEY", "zai": "ZAI_API_KEY", "kimi-coding": "KIMI_CODING_API_KEY",
	"minimax": "MINIMAX_API_KEY", "minimax-china": "MINIMAX_API_KEY", "moonshot": "MOONSHOT_API_KEY",
	"synthetic": "SYNTHETIC_API_KEY", "aihubmix": "AIHUBMIX_API_KEY",
	"alibaba-singapore": "ALIBABA_SINGAPORE_API_KEY", "alibaba-us": "ALIBABA_US_API_KEY",
	"atlascloud": "ATLASCLOUD_API_KEY", "baseten": "BASETEN_API_KEY", "avian": "AVIAN_API_KEY",
	"azure": "AZURE_OPENAI_API_KEY", "cerebras": "CEREBRAS_API_KEY", "chutes": "CHUTES_API_KEY",
	"coralbricks": "CORALBRICKS_API_KEY", "cortecs": "CORTECS_API_KEY", "deepseek": "DEEPSEEK_API_KEY",
	"fireworks": "FIREWORKS_API_KEY", "groq": "GROQ_API_KEY", "huggingface": "HF_TOKEN",
	"ionet": "IONET_API_KEY", "nebius": "NEBIUS_API_KEY", "neuralwatt": "NEURALWATT_API_KEY",
	"opencode-go": "OPENCODE_API_KEY", "opencode-zen": "OPENCODE_API_KEY",
	"openrouter": "OPENROUTER_API_KEY", "qiniucloud": "QINIUCLOUD_API_KEY", "scaleway": "SCW_SECRET_KEY",
	"vercel": "VERCEL_API_KEY", "venice": "VENICE_API_KEY", "zhipu": "ZHIPU_API_KEY",
	"zhipu-coding": "ZHIPU_API_KEY",
}

type crushProviderConfig struct {
	APIKey  string          `json:"api_key"`
	Type    string          `json:"type"`
	Disable *bool           `json:"disable"`
	OAuth   json.RawMessage `json:"oauth"`
}

var crushEnvRefPattern = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// crushConfiguredProviders reports the providers Crush can authenticate: a
// provider in crush.json (global config, global data, then the workspace's)
// with a literal api_key, an api_key whose $VAR references are all set, or a
// stored OAuth token; and a provider from Crush's provider list whose key
// variable is set, or, for Bedrock and Vertex AI, whose cloud credentials
// Crush itself requires are present. A disabled provider never counts, and an
// api_key that runs a $(command) is not run, so it is not counted.
func crushConfiguredProviders(workingDir string, env map[string]string) []string {
	home := credentialPresence(envValue(env, "HOME"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	present := func(name string) bool { return credentialPresence(envValue(env, name)) != "" }

	var paths []string
	if dir := envValue(env, "CRUSH_GLOBAL_CONFIG"); dir != "" {
		paths = append(paths, filepath.Join(dir, "crush.json"))
	} else if dir := xdgDir(home, env, "XDG_CONFIG_HOME", ".config"); dir != "" {
		paths = append(paths, filepath.Join(dir, "crush", "crush.json"))
	}
	dataDir := envValue(env, "CRUSH_GLOBAL_DATA")
	if dataDir == "" {
		if dir := xdgDir(home, env, "XDG_DATA_HOME", filepath.Join(".local", "share")); dir != "" {
			dataDir = filepath.Join(dir, "crush")
		}
	}
	if dataDir != "" {
		paths = append(paths, filepath.Join(dataDir, "crush.json"))
	}
	if workingDir != "" {
		paths = append(paths, filepath.Join(workingDir, "crush.json"), filepath.Join(workingDir, ".crush.json"))
	}

	// Later files override earlier ones field by field, as Crush merges them.
	configured := map[string]*crushProviderConfig{}
	for _, path := range paths {
		raw, err := readModelConfig(path)
		if err != nil {
			continue
		}
		var config struct {
			Providers map[string]crushProviderConfig `json:"providers"`
		}
		if json.Unmarshal(raw, &config) != nil {
			continue
		}
		for id, provider := range config.Providers {
			merged := configured[id]
			if merged == nil {
				merged = &crushProviderConfig{}
				configured[id] = merged
			}
			if strings.TrimSpace(provider.APIKey) != "" {
				merged.APIKey = provider.APIKey
			}
			if provider.Type != "" {
				merged.Type = provider.Type
			}
			if provider.Disable != nil {
				merged.Disable = provider.Disable
			}
			if len(provider.OAuth) > 0 && string(provider.OAuth) != "null" {
				merged.OAuth = provider.OAuth
			}
		}
	}

	known := map[string]crushProviderConfig{}
	for id, name := range crushProviderKeyEnv {
		known[id] = crushProviderConfig{APIKey: "$" + name}
	}
	known["bedrock"] = crushProviderConfig{Type: "bedrock"}
	known["bedrock-europe"] = crushProviderConfig{Type: "bedrock"}
	known["vertexai"] = crushProviderConfig{Type: "google-vertex"}
	if dataDir != "" {
		if raw, err := readModelConfig(filepath.Join(dataDir, "providers.json")); err == nil {
			var cached []struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				APIKey string `json:"api_key"`
			}
			if json.Unmarshal(raw, &cached) == nil && len(cached) > 0 {
				known = map[string]crushProviderConfig{}
				for _, provider := range cached {
					if id := strings.TrimSpace(provider.ID); id != "" {
						known[id] = crushProviderConfig{APIKey: provider.APIKey, Type: provider.Type}
					}
				}
			}
		}
	}

	found := map[string]bool{}
	for id, provider := range known {
		if override := configured[id]; override != nil {
			if override.Disable != nil && *override.Disable {
				continue
			}
			if override.APIKey != "" {
				provider.APIKey = override.APIKey
			}
		}
		if crushKeyResolves(provider.APIKey, present) || crushCloudCredentials(provider.Type, present) {
			found[id] = true
		}
	}
	for id, provider := range configured {
		if provider.Disable != nil && *provider.Disable {
			continue
		}
		if crushKeyResolves(provider.APIKey, present) || len(provider.OAuth) > 0 {
			found[id] = true
		}
	}
	providers := make([]string, 0, len(found))
	for id := range found {
		providers = append(providers, id)
	}
	return providers
}

// crushKeyResolves reports whether an api_key setting yields a key without
// running anything: a literal value, or $VAR references that are all set.
func crushKeyResolves(value string, present func(string) bool) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "$(") {
		return false
	}
	refs := crushEnvRefPattern.FindAllStringSubmatch(value, -1)
	for _, ref := range refs {
		if !present(ref[1]) {
			return false
		}
	}
	return len(refs) > 0 || credentialPresence(value) != ""
}

// crushCloudCredentials mirrors the credentials Crush requires before it
// offers Bedrock or Vertex AI, which have no API key of their own.
func crushCloudCredentials(providerType string, present func(string) bool) bool {
	switch providerType {
	case "bedrock":
		return present("AWS_BEARER_TOKEN_BEDROCK") || present("AWS_PROFILE") || present("AWS_DEFAULT_PROFILE") ||
			(present("AWS_ACCESS_KEY_ID") && present("AWS_SECRET_ACCESS_KEY"))
	case "google-vertex":
		return present("VERTEXAI_PROJECT") && present("VERTEXAI_LOCATION")
	}
	return false
}
