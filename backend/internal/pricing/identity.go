// Package pricing owns shared lookup identity rules for usage pricing.
package pricing

import (
	"regexp"
	"strconv"
	"strings"
)

// CanonicalProviderID normalizes a reported provider for exact catalog lookup.
func CanonicalProviderID(raw string) string {
	providerID := strings.ToLower(strings.TrimSpace(raw))
	if alias, ok := providerAliases[providerID]; ok {
		return alias
	}
	return providerID
}

var providerAliases = map[string]string{
	"z.ai":                   "zai",
	"google":                 "gemini",
	"google-gemini":          "gemini",
	"google_gemini":          "gemini",
	"google-vertex":          "vertex_ai",
	"google_vertex":          "vertex_ai",
	"vertex":                 "vertex_ai",
	"vertex-ai":              "vertex_ai",
	"vertexai":               "vertex_ai",
	"gcp_vertex_ai":          "vertex_ai",
	"amazon-bedrock":         "bedrock",
	"amazon_bedrock":         "bedrock",
	"aws-bedrock":            "bedrock",
	"aws_bedrock":            "bedrock",
	"bedrock_converse":       "bedrock",
	"azure-openai":           "azure",
	"azure_openai":           "azure",
	"azure-openai-responses": "azure",
	"github-copilot":         "github_copilot",
	"copilot":                "github_copilot",
	"vercel-ai-gateway":      "vercel_ai_gateway",
	"fireworks":              "fireworks_ai",
	"together":               "together_ai",
	"togetherai":             "together_ai",
	"moonshotai":             "moonshot",
	"mistralai":              "mistral",
	"x-ai":                   "xai",
	"deepseek-official":      "deepseek",
	"opencode-go":            "opencode_go",
	"xiaomi":                 "xiaomi_mimo",
	"cloudflare-workers-ai":  "cloudflare",
	"cohere":                 "cohere_chat",
	"friendli":               "friendliai",
	"lambda":                 "lambda_ai",
}

// RequiredProviderIDs are the providers every catalog must price.
var RequiredProviderIDs = []string{"anthropic", "openai", "zai"}

var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidProviderID reports whether id can name a catalog provider: it is also a path segment of the provider's blob.
func ValidProviderID(id string) bool {
	return len(id) <= 64 && providerIDPattern.MatchString(id)
}

// UnidentifiedBillingRoute marks a Claude route the hook saw and could not
// name: ANTHROPIC_BASE_URL pointed somewhere, but not at a host AO bills
// against. It is deliberately not a provider — it is the difference between
// "no hook has run yet" and "a hook ran and the answer is not one of ours",
// which an empty hint cannot express and which decides whether the model that
// answered is admissible evidence at all.
const UnidentifiedBillingRoute = "unidentified"

// TrustedClaudeBillingProvider narrows one routing string to a billing identity
// AO is willing to record for a Claude session. Anthropic, z.ai, Bedrock and
// Vertex are the four routes Claude Code can take that AO can name; anything
// else is a string of unknown provenance, and recording it is worse than
// recording nothing, because billing_provider_id is write-once and an
// unrecognised value there is unreachable by every later repair.
//
// Codex is deliberately not narrowed this way. Its rollout reports the provider
// its own config selected, which is a durable fact about who billed the session
// even when no catalog can price it, and blanking it would let the model
// fallback bill an OpenRouter or Azure session at OpenAI list rates.
func TrustedClaudeBillingProvider(raw string) string {
	providerID := CanonicalProviderID(raw)
	switch providerID {
	case "anthropic", "zai", "bedrock", "vertex_ai":
		return providerID
	default:
		return ""
	}
}

// TrustedClaudeRoute narrows one routing string to what may be recorded on a
// binding: a provider AO bills against, or the sentinel saying a route was
// observed and is not one of them.
func TrustedClaudeRoute(raw string) string {
	if provider := TrustedClaudeBillingProvider(raw); provider != "" {
		return provider
	}
	if CanonicalProviderID(raw) == UnidentifiedBillingRoute {
		return UnidentifiedBillingRoute
	}
	return ""
}

// CanonicalModelID normalizes a reported model for exact provider-local lookup.
// It removes at most one exact canonical provider prefix.
func CanonicalModelID(providerID, raw string) string {
	modelID := strings.ToLower(strings.TrimSpace(raw))
	prefix := CanonicalProviderID(providerID)
	if prefix != "" {
		modelID = strings.TrimPrefix(modelID, prefix+"/")
	}
	return modelID
}

// Service tiers the catalog prices apart from a model's standard rates.
const (
	ServiceTierPriority  = "priority"
	ServiceTierFlex      = "flex"
	ServiceTierUltrafast = "ultrafast"
	ServiceTierFast      = "fast"
)

// TierModelID names the catalog row that prices modelID at a service tier: "gpt-6.1-sol#priority".
func TierModelID(modelID, tier string) string {
	return modelID + "#" + tier
}

// LongContextModelID names the catalog row that prices modelID for a request with more than aboveK thousand input tokens: "gpt-6.1-sol#above272k", and "gpt-6.1-sol#priority#above272k" for a tier's row.
func LongContextModelID(modelID string, aboveK int64) string {
	return modelID + longContextMarker + strconv.FormatInt(aboveK, 10) + "k"
}

const longContextMarker = "#above"

func longContextThreshold(rowID string) (base string, aboveK int64, ok bool) {
	index := strings.LastIndex(rowID, longContextMarker)
	if index < 0 || !strings.HasSuffix(rowID, "k") {
		return "", 0, false
	}
	aboveK, err := strconv.ParseInt(rowID[index+len(longContextMarker):len(rowID)-1], 10, 64)
	if err != nil || aboveK <= 0 {
		return "", 0, false
	}
	return rowID[:index], aboveK, true
}
