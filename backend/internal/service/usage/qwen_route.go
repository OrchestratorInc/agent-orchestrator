package usage

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
	workspace := strings.TrimSpace(session.Metadata.WorkspacePath)
	qwenHome := filepath.Join(home, ".qwen")
	if configuredHome := strings.TrimSpace(os.Getenv("QWEN_HOME")); configuredHome != "" {
		qwenHome = resolveQwenStoragePath(configuredHome, home, workspace)
	}
	paths := []string{filepath.Join(qwenHome, "settings.json")}
	if workspace != "" {
		paths = append(paths, filepath.Join(workspace, ".qwen", "settings.json"))
	}
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
	envURL       string
	modelURLs    map[string]string // auth type + "/" + lower-case model id
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
	if value := strings.TrimSpace(settings.Env["OPENAI_BASE_URL"]); value != "" {
		r.envURL = value
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
		// A model the settings do not list goes to the default endpoint.
		baseURL = r.defaultURL
	}
	if baseURL == "" {
		baseURL = firstNonEmptyString(r.envURL, strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")))
	}
	if provider := qwenEndpointProvider(baseURL); provider != "" {
		return provider
	}
	switch r.authType {
	case "gemini":
		return "gemini"
	case "vertex-ai":
		return "vertex_ai"
	case "anthropic":
		if baseURL == "" {
			return "anthropic"
		}
	case "openai", "openai-responses":
		if baseURL == "" {
			return "openai"
		}
	}
	return ""
}

// qwenEndpointProvider maps an endpoint host to its pricing catalog provider.
func qwenEndpointProvider(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "":
		return ""
	case host == "api.z.ai" || strings.HasSuffix(host, ".z.ai"):
		return "zai"
	case host == "open.bigmodel.cn":
		// Zhipu's mainland platform, which LiteLLM prices apart from z.ai.
		return "bigmodel"
	case host == "api.openai.com":
		return "openai"
	case host == "api.anthropic.com":
		return "anthropic"
	case strings.HasPrefix(host, "dashscope") && strings.HasSuffix(host, ".aliyuncs.com"):
		return "dashscope"
	case host == "generativelanguage.googleapis.com":
		return "gemini"
	case host == "openrouter.ai":
		return "openrouter"
	case host == "api.deepseek.com":
		return "deepseek"
	case host == "api.moonshot.ai" || host == "api.moonshot.cn":
		return "moonshot"
	case host == "api.x.ai":
		return "xai"
	case host == "api.mistral.ai":
		return "mistral"
	case host == "api.groq.com":
		return "groq"
	case host == "api.minimax.io" || host == "api.minimaxi.com" || host == "api.minimax.chat":
		return "minimax"
	}
	return ""
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
