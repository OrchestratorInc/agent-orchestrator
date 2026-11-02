package modelcatalog

import (
	"encoding/json"
	"os"
)

func init() { browseProviderCredentials["autohand"] = autohandConfiguredProviders }

// autohandKeyedProviders are the config.json provider sections Autohand reads
// an apiKey from. Its local providers (Ollama, llama.cpp, MLX) need no key, so
// nothing on disk shows whether they are ready, and they are not marked.
var autohandKeyedProviders = []string{
	"openrouter", "openai", "llmgateway", "azure", "zai", "vertexai", "xai", "cerebras", "nvidia", "deepseek", "bedrock",
}

// autohandConfiguredProviders reports the provider sections of
// ~/.autohand/config.json holding an apiKey, and Azure when
// AZURE_OPENAI_KEY is set, which Autohand reads ahead of its config.
func autohandConfiguredProviders(_ string, env map[string]string) []string {
	var providers []string
	for _, path := range modelConfigPaths("autohand", "", env) {
		raw, err := readModelConfig(path)
		if err != nil {
			continue
		}
		var config map[string]json.RawMessage
		if json.Unmarshal(raw, &config) != nil {
			continue
		}
		for _, provider := range autohandKeyedProviders {
			var section struct {
				APIKey string `json:"apiKey"`
			}
			if config[provider] != nil && json.Unmarshal(config[provider], &section) == nil && credentialPresence(section.APIKey) != "" {
				providers = append(providers, provider)
			}
		}
	}
	azureKey, ok := env["AZURE_OPENAI_KEY"]
	if !ok {
		azureKey = os.Getenv("AZURE_OPENAI_KEY")
	}
	if credentialPresence(azureKey) != "" {
		providers = append(providers, "azure")
	}
	return uniqueProviders(providers)
}
