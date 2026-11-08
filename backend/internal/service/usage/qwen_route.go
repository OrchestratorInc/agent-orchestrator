package usage

import (
	"cmp"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

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

type qwenRoute struct {
	authType     string
	defaultModel string
	defaultURL   string
	authURL      string
	envURLs      map[string]string
	modelURLs    map[string]string
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
		baseURL = cmp.Or(strings.TrimSpace(r.defaultURL), strings.TrimSpace(r.authURL))
	}
	if baseURL == "" {
		envName := "OPENAI_BASE_URL"
		if r.authType == "anthropic" {
			envName = "ANTHROPIC_BASE_URL"
		}
		baseURL = cmp.Or(strings.TrimSpace(r.envURLs[envName]), strings.TrimSpace(os.Getenv(envName)))
	}
	if baseURL != "" {
		return qwenEndpointProvider(baseURL)
	}
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

func qwenEndpointProvider(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || privateQwenHost(host) {
		return ""
	}
	if strings.HasSuffix(host, "aiplatform.googleapis.com") {
		return "vertex_ai"
	}
	for _, rule := range qwenHostRules {
		if host == rule.host || strings.HasSuffix(host, "."+rule.host) {
			return rule.provider
		}
	}
	registered, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return ""
	}
	label, _, _ := strings.Cut(registered, ".")
	provider := pricing.CanonicalProviderID(label)
	if strings.HasSuffix(host, ".cn") {
		provider += "-cn"
	}
	return provider
}

var qwenHostRules = []struct{ host, provider string }{
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
	{"z.ai", "zai"},
	{"open.bigmodel.cn", "bigmodel"},
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
	{"gateway.ai.cloudflare.com", "cloudflare-ai-gateway"},
	{"api.cloudflare.com", "cloudflare"},
	{"lambdalabs.com", "lambda_ai"},
}

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
