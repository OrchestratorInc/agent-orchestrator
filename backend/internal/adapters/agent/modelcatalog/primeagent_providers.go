package modelcatalog

import (
	"os"
	"path/filepath"
	"strings"
)

func init() { browseProviderCredentials["prime-agent"] = primeAgentConfiguredProviders }

// primeAgentKeyEnv mirrors Prime Agent's built-in provider credential
// variables (its Pi runtime's getApiKeyEnvVars), first match wins there.
var primeAgentKeyEnv = map[string][]string{
	"github-copilot":         {"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"},
	"anthropic":              {"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
	"openai":                 {"OPENAI_API_KEY"},
	"azure-openai-responses": {"AZURE_OPENAI_API_KEY"},
	"prime-inference":        {"PRIME_API_KEY"},
	"deepseek":               {"DEEPSEEK_API_KEY"},
	"google":                 {"GEMINI_API_KEY"},
	"google-vertex":          {"GOOGLE_CLOUD_API_KEY"},
	"groq":                   {"GROQ_API_KEY"},
	"cerebras":               {"CEREBRAS_API_KEY"},
	"xai":                    {"XAI_API_KEY"},
	"openrouter":             {"OPENROUTER_API_KEY"},
	"vercel-ai-gateway":      {"AI_GATEWAY_API_KEY"},
	"zai":                    {"ZAI_API_KEY"},
	"mistral":                {"MISTRAL_API_KEY"},
	"minimax":                {"MINIMAX_API_KEY"},
	"minimax-cn":             {"MINIMAX_CN_API_KEY"},
	"moonshotai":             {"MOONSHOT_API_KEY"},
	"moonshotai-cn":          {"MOONSHOT_API_KEY"},
	"huggingface":            {"HF_TOKEN"},
	"fireworks":              {"FIREWORKS_API_KEY"},
	"opencode":               {"OPENCODE_API_KEY"},
	"opencode-go":            {"OPENCODE_API_KEY"},
	"kimi-coding":            {"KIMI_API_KEY"},
	"cloudflare-workers-ai":  {"CLOUDFLARE_API_KEY"},
	"cloudflare-ai-gateway":  {"CLOUDFLARE_API_KEY"},
	"xiaomi":                 {"XIAOMI_API_KEY"},
	"xiaomi-token-plan-cn":   {"XIAOMI_TOKEN_PLAN_CN_API_KEY"},
	"xiaomi-token-plan-ams":  {"XIAOMI_TOKEN_PLAN_AMS_API_KEY"},
	"xiaomi-token-plan-sgp":  {"XIAOMI_TOKEN_PLAN_SGP_API_KEY"},
}

// primeAgentConfiguredProviders reports the providers Prime Agent holds a
// credential for: logins and keys in its agent directory's auth.json, keyed
// custom providers in models.json, its built-in providers' variables, and the
// ambient AWS and Google Cloud credentials Bedrock and Vertex accept.
func primeAgentConfiguredProviders(_ string, env map[string]string) []string {
	found := map[string]bool{}
	if dir := primeAgentDir(env); dir != "" {
		for _, provider := range piAuthStoreProviders(filepath.Join(dir, "auth.json"), env) {
			found[provider] = true
		}
		for _, provider := range piModelsConfigProviders(filepath.Join(dir, "models.json"), env).configured {
			found[provider] = true
		}
	}
	for provider, names := range primeAgentKeyEnv {
		for _, name := range names {
			if piCredentialEnv(env, name) != "" {
				found[provider] = true
				break
			}
		}
	}
	set := func(name string) bool { return piCredentialEnv(env, name) != "" }
	if set("AWS_PROFILE") || (set("AWS_ACCESS_KEY_ID") && set("AWS_SECRET_ACCESS_KEY")) || set("AWS_BEARER_TOKEN_BEDROCK") ||
		set("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") || set("AWS_CONTAINER_CREDENTIALS_FULL_URI") || set("AWS_WEB_IDENTITY_TOKEN_FILE") {
		found["amazon-bedrock"] = true
	}
	if (set("GOOGLE_CLOUD_PROJECT") || set("GCLOUD_PROJECT")) && set("GOOGLE_CLOUD_LOCATION") && primeAgentVertexADC(env) {
		found["google-vertex"] = true
	}
	return sortedProviderSet(found)
}

// primeAgentDir mirrors Prime's getAgentDir: its own override (with tilde
// expansion), then ~/.prime/agent. Pi's override belongs to another harness.
func primeAgentDir(env map[string]string) string {
	home := catalogHome(env)
	if dir := envValue(env, "PRIME_AGENT_CODING_AGENT_DIR"); dir != "" {
		if dir == "~" || strings.HasPrefix(dir, "~/") {
			if home == "" {
				return ""
			}
			return home + strings.TrimPrefix(dir, "~")
		}
		return dir
	}
	if home != "" {
		return filepath.Join(home, ".prime", "agent")
	}
	return ""
}

func primeAgentVertexADC(env map[string]string) bool {
	path := envValue(env, "GOOGLE_APPLICATION_CREDENTIALS")
	if path == "" {
		home := catalogHome(env)
		if home == "" {
			return false
		}
		path = filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	}
	_, err := os.Stat(path)
	return err == nil
}
