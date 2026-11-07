package usage

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

// qwenProviderHint names the provider that bills a Qwen session. Qwen's usage
// log records only its auth type ("openai" for every OpenAI-compatible
// endpoint), so the route lives in Qwen's settings: the base URL of the
// configured model, or of the selected auth type's default model. Workspace
// settings override user settings, as they do for Qwen itself. An endpoint AO
// cannot name yields "", which leaves the session's cost unknown rather than
// priced against the wrong provider.
func (c *Collector) qwenProviderHint(ctx context.Context, session domain.SessionRecord) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	_, paths := qwenSettingsPaths(home, strings.TrimSpace(session.Metadata.WorkspacePath))
	var route qwenRoute
	for _, path := range paths {
		if ctx.Err() != nil {
			return ""
		}
		data, err := os.ReadFile(path) //nolint:gosec // Qwen's own settings paths.
		if err != nil {
			continue
		}
		route.merge(data)
	}
	model, authType := splitQwenModelID(session.Metadata.Model)
	return route.provider(model, authType)
}

// splitQwenModelID separates the auth type Qwen's ACP model IDs carry
// ("GLM-5.2(openai)") from the model ID itself.
func splitQwenModelID(raw string) (model, authType string) {
	raw = strings.TrimSpace(raw)
	open := strings.LastIndex(raw, "(")
	if open > 0 && strings.HasSuffix(raw, ")") {
		return strings.TrimSpace(raw[:open]), strings.TrimSpace(raw[open+1 : len(raw)-1])
	}
	return raw, ""
}

type qwenSettings struct {
	Model struct {
		Name    string `json:"name"`
		BaseURL string `json:"baseUrl"`
	} `json:"model"`
	Security struct {
		Auth struct {
			SelectedType string `json:"selectedType"`
			BaseURL      string `json:"baseUrl"`
		} `json:"auth"`
	} `json:"security"`
	Env            map[string]string `json:"env"`
	ModelProviders map[string][]struct {
		ID      string `json:"id"`
		BaseURL string `json:"baseUrl"`
	} `json:"modelProviders"`
}

// qwenRoute is the merged routing view of Qwen's settings files.
type qwenRoute struct {
	authType     string
	defaultModel string
	defaultURL   string
	authURL      string // security.auth.baseUrl, which Qwen still reads
	envURLs      map[string]string
	// modelURLs is keyed by provider id + "/" + lower-case model id. The
	// provider id is an auth type or a custom id Qwen maps to one.
	modelURLs map[string]string
}

func (r *qwenRoute) merge(data []byte) {
	var settings qwenSettings
	if json.Unmarshal(data, &settings) != nil {
		return
	}
	if value := strings.TrimSpace(settings.Security.Auth.SelectedType); value != "" {
		r.authType = value
	}
	if value := strings.TrimSpace(settings.Model.Name); value != "" {
		r.defaultModel = value
	}
	if value := strings.TrimSpace(settings.Model.BaseURL); value != "" {
		r.defaultURL = value
	}
	if value := strings.TrimSpace(settings.Security.Auth.BaseURL); value != "" {
		r.authURL = value
	}
	for _, name := range []string{"OPENAI_BASE_URL", "ANTHROPIC_BASE_URL"} {
		if value := strings.TrimSpace(settings.Env[name]); value != "" {
			if r.envURLs == nil {
				r.envURLs = make(map[string]string)
			}
			r.envURLs[name] = value
		}
	}
	for authType, models := range settings.ModelProviders {
		for _, model := range models {
			if strings.TrimSpace(model.ID) == "" || strings.TrimSpace(model.BaseURL) == "" {
				continue
			}
			if r.modelURLs == nil {
				r.modelURLs = make(map[string]string)
			}
			r.modelURLs[authType+"/"+strings.ToLower(strings.TrimSpace(model.ID))] = strings.TrimSpace(model.BaseURL)
		}
	}
}

// provider resolves the route for a session's model. Qwen writes the model a
// session selects back into the shared settings file, so the default model and
// its base URL describe whichever session chose last: a session that names a
// configured model is resolved from that model's own entry, and the collector
// keeps the first route it resolved for a binding rather than re-reading it.
func (r qwenRoute) provider(model, authType string) string {
	if authType != "" {
		r.authType = authType
	}
	if model == "" {
		model = r.defaultModel
	}
	baseURL := r.modelURLs[r.authType+"/"+strings.ToLower(model)]
	if baseURL == "" {
		baseURL = r.onlyURLFor(model)
	}
	if baseURL == "" {
		// A model the settings do not list goes to the default endpoint.
		baseURL = firstNonEmptyString(r.defaultURL, r.authURL)
	}
	if baseURL == "" {
		envName := "OPENAI_BASE_URL"
		if r.authType == "anthropic" {
			envName = "ANTHROPIC_BASE_URL"
		}
		baseURL = firstNonEmptyString(r.envURLs[envName], strings.TrimSpace(os.Getenv(envName)))
	}
	if baseURL != "" {
		// An endpoint names its provider, or leaves the route unknown: an
		// auth type is no evidence once a base URL points elsewhere.
		return qwenEndpointProvider(baseURL)
	}
	// No base URL anywhere: the auth type's SDK talks to its own provider.
	switch r.authType {
	case "gemini":
		return "gemini"
	case "vertex-ai":
		return "vertex_ai"
	case "anthropic":
		return "anthropic"
	case "openai", "openai-responses":
		return "openai"
	}
	return ""
}

// onlyURLFor finds a model under any provider id, for a model that a custom
// provider id (mapped to a protocol by providerProtocol) declares. It answers
// only when every entry for that model agrees on one endpoint.
func (r qwenRoute) onlyURLFor(model string) string {
	found := ""
	suffix := "/" + strings.ToLower(model)
	for key, baseURL := range r.modelURLs {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		if found != "" && found != baseURL {
			return ""
		}
		found = baseURL
	}
	return found
}

// qwenEndpointProvider names the provider that bills an endpoint. Qwen can
// point at any OpenAI-, Anthropic- or Gemini-compatible server, so a fixed host
// list would leave most of them unnamed: an API host is named after its
// provider (api.fireworks.ai, api.together.xyz), and that name, in the
// catalog's spelling, is the route. Hosts whose name says something else, and
// endpoints whose list price is not the provider's (a mainland-China region, a
// subscription plan), are named explicitly. A local or private server has no
// list price and yields "".
func qwenEndpointProvider(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || privateQwenHost(host) {
		return ""
	}
	// Vertex's regional hosts prefix the region with a dash.
	if strings.HasSuffix(host, "aiplatform.googleapis.com") {
		return "vertex_ai"
	}
	for _, rule := range qwenHostRules {
		if host == rule.host || strings.HasSuffix(host, "."+rule.host) {
			return rule.provider
		}
	}
	label, mainland := registrableLabel(host)
	if label == "" {
		return ""
	}
	provider := pricing.CanonicalProviderID(label)
	if mainland {
		// Mainland endpoints bill in their own price list.
		provider += "-cn"
	}
	return provider
}

// qwenHostRules name hosts the generic rule would get wrong, longest suffix
// first where two overlap.
var qwenHostRules = []struct{ host, provider string }{
	// Alibaba: pay-as-you-go DashScope internationally and in Beijing, and the
	// Coding and Token plans, which bill a subscription rather than tokens.
	{"coding.dashscope.aliyuncs.com", "dashscope-coding-plan-cn"},
	{"coding-intl.dashscope.aliyuncs.com", "dashscope-coding-plan"},
	{"maas.aliyuncs.com", "dashscope-token-plan"},
	{"token-plan.maas.qwencloudapi.com", "qwencloud-token-plan"},
	{"token-plan.maas.qianwenaiapi.com", "qwencloud-token-plan-cn"},
	{"dashscope-intl.aliyuncs.com", "dashscope"},
	{"dashscope-us.aliyuncs.com", "dashscope"},
	{"dashscope.aliyuncs.com", "dashscope-cn"},
	{"qwencloudapi.com", "qwencloud"},
	{"qianwenaiapi.com", "qwencloud-cn"},
	// Zhipu: z.ai internationally, bigmodel on the mainland platform.
	{"z.ai", "zai"},
	{"open.bigmodel.cn", "bigmodel"},
	// MiniMax and Moonshot run separate mainland platforms.
	{"api.minimaxi.com", "minimax-cn"},
	{"api.minimax.chat", "minimax-cn"},
	{"x.ai", "xai"},
	{"generativelanguage.googleapis.com", "gemini"},
	{"openai.azure.com", "azure"},
	{"services.ai.azure.com", "azure_ai"},
	{"models.ai.azure.com", "azure_ai"},
	{"amazonaws.com", "bedrock"},
	{"githubcopilot.com", "github_copilot"},
	{"ai-gateway.vercel.sh", "vercel_ai_gateway"},
	// Cloudflare's gateway forwards to any upstream; Workers AI is its own.
	{"gateway.ai.cloudflare.com", "cloudflare-ai-gateway"},
	{"api.cloudflare.com", "cloudflare"},
	{"lambdalabs.com", "lambda_ai"},
}

// registrableLabel returns the name a host registers under (fireworks for
// api.fireworks.ai) and whether it sits under China's country domain.
func registrableLabel(host string) (string, bool) {
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", false
	}
	tld := labels[len(labels)-1]
	suffixLen := 1
	// Second-level registries: example.com.cn, example.co.uk.
	if len(labels) >= 3 {
		switch labels[len(labels)-2] {
		case "com", "co", "net", "org", "ac", "gov", "edu":
			suffixLen = 2
		}
	}
	if len(labels) <= suffixLen {
		return "", false
	}
	return labels[len(labels)-1-suffixLen], tld == "cn"
}

// privateQwenHost reports a server with no public list price: a loopback or
// private address, or a name that only resolves on a private network.
func privateQwenHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home.arpa", ".test", ".example"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// stickyQwenProviderHint returns the route already recorded for the session's
// Qwen binding, resolving it from settings only when none is. Qwen rewrites the
// shared settings whenever any session selects a model, so a later hook could
// otherwise relabel this session with another session's provider.
func (c *Collector) stickyQwenProviderHint(ctx context.Context, session domain.SessionRecord) (string, error) {
	bindings, err := c.store.ListUsageBindingsForSession(ctx, session.ID)
	if err != nil {
		return "", err
	}
	for _, binding := range bindings {
		if binding.Harness == domain.HarnessQwen && strings.TrimSpace(binding.ProviderHint) != "" {
			return binding.ProviderHint, nil
		}
	}
	return c.qwenProviderHint(ctx, session), nil
}
