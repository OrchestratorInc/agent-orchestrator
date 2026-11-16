package modelcatalog

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func init() { browseProviderCredentials["aider"] = aiderConfiguredProviders }

// aiderProviderKeys is the credential each LiteLLM provider in Aider's catalog
// needs, from LiteLLM's validate_environment and, for providers it does not
// list, the variable the provider's own LiteLLM client reads. Each inner list
// must be fully set; any one list is enough. Providers whose credentials
// LiteLLM does not name (GitHub Copilot's OAuth, Meta Llama, OCI and the
// catalog's metadata-only providers) are never marked.
var aiderProviderKeys = map[string][][]string{
	"anthropic":         {{"ANTHROPIC_API_KEY"}},
	"openai":            {{"OPENAI_API_KEY"}},
	"azure":             {{"AZURE_API_KEY", "AZURE_API_BASE", "AZURE_API_VERSION"}},
	"azure_ai":          {{"AZURE_AI_API_KEY"}},
	"replicate":         {{"REPLICATE_API_KEY"}},
	"openrouter":        {{"OPENROUTER_API_KEY"}},
	"vercel_ai_gateway": {{"VERCEL_AI_GATEWAY_API_KEY"}},
	"vertex_ai":         {{"VERTEXAI_PROJECT", "VERTEXAI_LOCATION"}},
	"ai21":              {{"AI21_API_KEY"}},
	"together_ai":       {{"TOGETHERAI_API_KEY"}},
	"baseten":           {{"BASETEN_API_KEY"}},
	"bedrock":           {{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}},
	"bedrock_converse":  {{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}},
	"bedrock_mantle":    {{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}},
	"ollama":            {{"OLLAMA_API_BASE"}},
	"anyscale":          {{"ANYSCALE_API_KEY"}},
	"deepinfra":         {{"DEEPINFRA_API_KEY"}},
	"gemini":            {{"GEMINI_API_KEY"}, {"GOOGLE_API_KEY"}},
	"groq":              {{"GROQ_API_KEY"}},
	"cerebras":          {{"CEREBRAS_API_KEY"}},
	"xai":               {{"XAI_API_KEY"}},
	"deepseek":          {{"DEEPSEEK_API_KEY"}},
	"mistral":           {{"MISTRAL_API_KEY"}},
	"perplexity":        {{"PERPLEXITYAI_API_KEY"}},
	"cloudflare":        {{"CLOUDFLARE_API_KEY", "CLOUDFLARE_ACCOUNT_ID"}, {"CLOUDFLARE_API_KEY", "CLOUDFLARE_API_BASE"}},
	"novita":            {{"NOVITA_API_KEY"}},
	"nebius":            {{"NEBIUS_API_KEY"}},
	"wandb":             {{"WANDB_API_KEY"}},
	"dashscope":         {{"DASHSCOPE_API_KEY"}},
	"moonshot":          {{"MOONSHOT_API_KEY"}},
	"zai":               {{"ZAI_API_KEY"}},
	"minimax":           {{"MINIMAX_API_KEY"}},
	"gradient_ai":       {{"GRADIENT_AI_API_KEY"}},
	"hyperbolic":        {{"HYPERBOLIC_API_KEY"}},
	"lambda_ai":         {{"LAMBDA_API_KEY"}},
	"nscale":            {{"NSCALE_API_KEY"}},
	"ovhcloud":          {{"OVHCLOUD_API_KEY"}},
	"sambanova":         {{"SAMBANOVA_API_KEY"}},
	"v0":                {{"V0_API_KEY"}},
	"snowflake":         {{"SNOWFLAKE_JWT", "SNOWFLAKE_ACCOUNT_ID"}},
}

// aiderVertexCatalogPrefix groups the catalog's per-publisher Vertex AI
// providers (vertex_ai-language-models and so on) under Vertex's credential.
const aiderVertexCatalogPrefix = "vertex_ai-"

// aiderConfig holds the .aider.conf.yml options that supply credentials.
type aiderConfig struct {
	EnvFile         string
	OpenAIAPIKey    string
	AnthropicAPIKey string
	APIKey          []string
	SetEnv          []string
}

// aiderConfiguredProviders reports the providers Aider has a credential for,
// layered the way Aider applies them at launch: the environment, then its
// dotenv files (~/.aider/oauth-keys.env, ~/.env, the workspace's .env and the
// configured env-file, each overriding the last), then .aider.conf.yml's
// set-env, api-key and openai/anthropic-api-key settings.
func aiderConfiguredProviders(workingDir string, env map[string]string) []string {
	home := credentialPresence(envValue(env, "HOME"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	var config aiderConfig
	// Aider reads .aider.conf.yml from the workspace, then home; the first
	// file setting a key wins.
	var configPaths []string
	if workingDir != "" {
		configPaths = append(configPaths, filepath.Join(workingDir, ".aider.conf.yml"))
	}
	if home != "" {
		configPaths = append(configPaths, filepath.Join(home, ".aider.conf.yml"))
	}
	for _, path := range configPaths {
		raw, err := readModelConfig(path)
		if err != nil {
			continue
		}
		var file aiderConfig
		if aiderUnmarshalConfig(raw, &file) != nil {
			continue
		}
		config.EnvFile = aiderFirstNonEmpty(config.EnvFile, file.EnvFile)
		config.OpenAIAPIKey = aiderFirstNonEmpty(config.OpenAIAPIKey, file.OpenAIAPIKey)
		config.AnthropicAPIKey = aiderFirstNonEmpty(config.AnthropicAPIKey, file.AnthropicAPIKey)
		if len(config.APIKey) == 0 {
			config.APIKey = file.APIKey
		}
		if len(config.SetEnv) == 0 {
			config.SetEnv = file.SetEnv
		}
	}
	// AIDER_-prefixed variables set the same options as the config file and
	// take precedence over it.
	if value := envValue(env, "AIDER_ENV_FILE"); value != "" {
		config.EnvFile = value
	}
	if value := credentialPresence(envValue(env, "AIDER_OPENAI_API_KEY")); value != "" {
		config.OpenAIAPIKey = value
	}
	if value := credentialPresence(envValue(env, "AIDER_ANTHROPIC_API_KEY")); value != "" {
		config.AnthropicAPIKey = value
	}

	overrides := map[string]string{}
	var dotenvFiles []string
	if home != "" {
		dotenvFiles = append(dotenvFiles, filepath.Join(home, ".aider", "oauth-keys.env"), filepath.Join(home, ".env"))
	}
	if workingDir != "" {
		dotenvFiles = append(dotenvFiles, filepath.Join(workingDir, ".env"))
	}
	if file := strings.TrimSpace(config.EnvFile); file != "" {
		if file == "~" || strings.HasPrefix(file, "~/") {
			file = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(file, "~"), "/"))
		} else if !filepath.IsAbs(file) && workingDir != "" {
			file = filepath.Join(workingDir, file)
		}
		if filepath.IsAbs(file) {
			dotenvFiles = append(dotenvFiles, file)
		}
	}
	for _, path := range dotenvFiles {
		raw, err := readModelConfig(path)
		if err != nil {
			continue
		}
		for name, value := range parseDotenv(raw) {
			overrides[name] = value
		}
	}
	for _, setting := range config.SetEnv {
		if name, value, ok := strings.Cut(setting, "="); ok {
			overrides[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	for _, setting := range config.APIKey {
		if provider, value, ok := strings.Cut(setting, "="); ok {
			overrides[strings.ToUpper(strings.TrimSpace(provider))+"_API_KEY"] = strings.TrimSpace(value)
		}
	}
	if config.OpenAIAPIKey != "" {
		overrides["OPENAI_API_KEY"] = config.OpenAIAPIKey
	}
	if config.AnthropicAPIKey != "" {
		overrides["ANTHROPIC_API_KEY"] = config.AnthropicAPIKey
	}

	present := func(name string) bool {
		if value, ok := overrides[name]; ok {
			return credentialPresence(value) != ""
		}
		return credentialPresence(envValue(env, name)) != ""
	}
	satisfied := func(sets [][]string) bool {
		for _, set := range sets {
			all := true
			for _, name := range set {
				if !present(name) {
					all = false
					break
				}
			}
			if all {
				return true
			}
		}
		return false
	}
	var providers []string
	for provider, sets := range aiderProviderKeys {
		if satisfied(sets) {
			providers = append(providers, provider)
		}
	}
	if satisfied(aiderProviderKeys["vertex_ai"]) {
		for _, suffix := range []string{"ai21_models", "deepseek_models", "language-models", "llama_models", "zai_models"} {
			providers = append(providers, aiderVertexCatalogPrefix+suffix)
		}
	}
	return providers
}

// aiderUnmarshalConfig accepts api-key and set-env as a single string or a
// list, as Aider's config file does.
func aiderUnmarshalConfig(raw []byte, config *aiderConfig) error {
	var values map[string]any
	if err := yaml.Unmarshal(raw, &values); err != nil {
		return err
	}
	text := func(key string) string {
		value, _ := values[key].(string)
		return strings.TrimSpace(value)
	}
	list := func(key string) []string {
		switch value := values[key].(type) {
		case string:
			return []string{value}
		case []any:
			var out []string
			for _, item := range value {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
		return nil
	}
	config.EnvFile = text("env-file")
	config.OpenAIAPIKey = text("openai-api-key")
	config.AnthropicAPIKey = text("anthropic-api-key")
	config.APIKey = list("api-key")
	config.SetEnv = list("set-env")
	return nil
}

func aiderFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
