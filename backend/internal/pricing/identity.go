package pricing

import (
	"regexp"
	"strconv"
	"strings"
)

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

var RequiredProviderIDs = []string{"anthropic", "openai", "zai"}

var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func ValidProviderID(id string) bool {
	return len(id) <= 64 && providerIDPattern.MatchString(id)
}

const UnidentifiedBillingRoute = "unidentified"

func TrustedClaudeBillingProvider(raw string) string {
	providerID := CanonicalProviderID(raw)
	switch providerID {
	case "anthropic", "zai", "bedrock", "vertex_ai":
		return providerID
	default:
		return ""
	}
}

func TrustedClaudeRoute(raw string) string {
	if provider := TrustedClaudeBillingProvider(raw); provider != "" {
		return provider
	}
	if CanonicalProviderID(raw) == UnidentifiedBillingRoute {
		return UnidentifiedBillingRoute
	}
	return ""
}

func CanonicalModelID(providerID, raw string) string {
	modelID := strings.ToLower(strings.TrimSpace(raw))
	prefix := CanonicalProviderID(providerID)
	if prefix != "" {
		modelID = strings.TrimPrefix(modelID, prefix+"/")
	}
	return modelID
}

const (
	ServiceTierPriority  = "priority"
	ServiceTierFlex      = "flex"
	ServiceTierUltrafast = "ultrafast"
	ServiceTierFast      = "fast"
)

func TierModelID(modelID, tier string) string {
	return modelID + "#" + tier
}

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
