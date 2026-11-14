package modelcatalog

import (
	"encoding/json"
	"os"
)

func init() { browseProviderCredentials["autohand"] = autohandConfiguredProviders }

var autohandKeyedProviders = []string{
	"openrouter", "openai", "llmgateway", "azure", "zai", "vertexai", "xai", "cerebras", "nvidia", "deepseek", "bedrock",
}

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
