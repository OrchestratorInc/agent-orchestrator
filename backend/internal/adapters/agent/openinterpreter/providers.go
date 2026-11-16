package openinterpreter

import (
	"strings"
)

// GeneratedProvider is one of Open Interpreter's built-in model providers that
// reads its credential from an environment variable. Native sign-in covers
// only the default OpenAI provider; these are selected with
// `-c model_provider=<ID>`.
type GeneratedProvider struct {
	ID     string
	Label  string
	EnvKey string
}

// GeneratedProviders lists the providers whose keys AO offers for Open
// Interpreter, in the order AO shows them. IDs are the native provider names
// accepted by `interpreter debug models -c model_provider=<ID>`.
var GeneratedProviders = []GeneratedProvider{
	{ID: "anthropic", Label: "Anthropic", EnvKey: "ANTHROPIC_API_KEY"},
	{ID: "google", Label: "Gemini", EnvKey: "GEMINI_API_KEY"},
	{ID: "openrouter", Label: "OpenRouter", EnvKey: "OPENROUTER_API_KEY"},
	{ID: "deepseek", Label: "DeepSeek", EnvKey: "DEEPSEEK_API_KEY"},
}

// providerModelSeparator joins a generated provider ID and its native model
// slug in an AO model ID. Slugs may contain "/" (OpenRouter), so ":" is used,
// and only a known provider ID before the first ":" selects a provider.
const providerModelSeparator = ":"

// ProviderModelID is the AO model ID for slug on a generated provider.
func ProviderModelID(provider, slug string) string {
	return provider + providerModelSeparator + slug
}

// SplitProviderModel separates an AO model ID into a generated provider and
// its native slug. An ID without a known provider prefix belongs to the
// active native provider and is returned unchanged with ok false.
func SplitProviderModel(model string) (provider, slug string, ok bool) {
	prefix, rest, found := strings.Cut(model, providerModelSeparator)
	if !found || strings.TrimSpace(rest) == "" {
		return "", model, false
	}
	for _, candidate := range GeneratedProviders {
		if prefix == candidate.ID {
			return prefix, rest, true
		}
	}
	return "", model, false
}

func generatedProviderKeyNames() []string {
	names := make([]string, 0, len(GeneratedProviders))
	for _, provider := range GeneratedProviders {
		names = append(names, provider.EnvKey)
	}
	return names
}
