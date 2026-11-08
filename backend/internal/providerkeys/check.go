package providerkeys

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Check statuses. A key is "unverified" when AO has no reviewed endpoint for
// its provider, and "unreachable" when the provider could not answer.
const (
	CheckValid       = "valid"
	CheckInvalid     = "invalid"
	CheckUnverified  = "unverified"
	CheckUnreachable = "unreachable"
)

// CheckResult reports whether a provider accepted a key, with the model IDs
// that key can list. It never carries the key.
type CheckResult struct {
	Status string   `json:"status" enum:"valid,invalid,unverified,unreachable"`
	Models []string `json:"models,omitempty"`
}

// checkEndpoint is one provider's reviewed, fixed model-list request. The key
// is sent only to url (and validateURL, when the list itself is public).
type checkEndpoint struct {
	url string
	// validateURL, when set, authenticates the key because url is public.
	validateURL string
	authorize   func(*http.Request, string)
	// prefix is stripped from each listed ID (Gemini lists "models/<id>").
	prefix string
	// rejectsWith400 marks providers that answer a bad key with HTTP 400
	// (Gemini's API_KEY_INVALID, xAI) instead of 401 or 403.
	rejectsWith400 bool
	// regional marks providers whose keys belong to one region's endpoint, so
	// a key this endpoint rejects may still be valid at another.
	regional bool
}

func bearer(req *http.Request, key string) { req.Header.Set("Authorization", "Bearer "+key) }

// checkEndpoints is a package variable so tests can point it at a local server.
var checkEndpoints = map[string]checkEndpoint{
	"ANTHROPIC_API_KEY": {url: "https://api.anthropic.com/v1/models?limit=1000", authorize: func(req *http.Request, key string) {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}},
	"OPENAI_API_KEY": {url: "https://api.openai.com/v1/models", authorize: bearer},
	"GEMINI_API_KEY": {url: "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000", prefix: "models/", rejectsWith400: true, authorize: func(req *http.Request, key string) {
		req.Header.Set("x-goog-api-key", key)
	}},
	"OPENROUTER_API_KEY": {url: "https://openrouter.ai/api/v1/models", validateURL: "https://openrouter.ai/api/v1/key", authorize: bearer},
	"DEEPSEEK_API_KEY":   {url: "https://api.deepseek.com/models", authorize: bearer},
	"GROQ_API_KEY":       {url: "https://api.groq.com/openai/v1/models", authorize: bearer},
	"XAI_API_KEY":        {url: "https://api.x.ai/v1/models", authorize: bearer, rejectsWith400: true},
	"MISTRAL_API_KEY":    {url: "https://api.mistral.ai/v1/models", authorize: bearer},
	"FIREWORKS_API_KEY":  {url: "https://api.fireworks.ai/inference/v1/models", authorize: bearer},
	// Moonshot's .ai and .cn platforms issue separate keys.
	"MOONSHOT_API_KEY": {url: "https://api.moonshot.ai/v1/models", authorize: bearer, regional: true},
	// Matches the China endpoint the Qwen DashScope method configures; an
	// international key is not accepted there.
	"DASHSCOPE_API_KEY": {url: "https://dashscope.aliyuncs.com/compatible-mode/v1/models", authorize: bearer, regional: true},
}

// checkClient never follows a redirect: Go drops Authorization on a
// cross-host hop but would re-send x-api-key or x-goog-api-key, and follow an
// https-to-http downgrade. A redirect reads as unreachable.
var checkClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

const (
	// OpenRouter's full catalog alone runs to several megabytes.
	maxCheckBody   = 32 << 20
	maxCheckModels = 1000
)

// Checkable reports whether AO can ask name's provider to accept a key.
func Checkable(name string) bool {
	_, ok := checkEndpoints[name]
	return ok
}

// Check asks name's provider whether it accepts value and lists its models.
func Check(ctx context.Context, name, value string) (CheckResult, error) {
	if !validName(name) || !validValue(value) || value == "" {
		return CheckResult{}, errors.New("invalid provider credential")
	}
	endpoint, ok := checkEndpoints[name]
	if !ok {
		return CheckResult{Status: CheckUnverified}, nil
	}
	if endpoint.validateURL != "" {
		status, _ := fetch(ctx, endpoint.validateURL, endpoint, value)
		if status != CheckValid {
			return CheckResult{Status: status}, nil
		}
	}
	status, body := fetch(ctx, endpoint.url, endpoint, value)
	if status != CheckValid {
		return CheckResult{Status: status}, nil
	}
	return CheckResult{Status: CheckValid, Models: modelIDs(body, endpoint.prefix)}, nil
}

func fetch(ctx context.Context, url string, endpoint checkEndpoint, key string) (string, []byte) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return CheckUnreachable, nil
	}
	endpoint.authorize(req, key)
	req.Header.Set("Accept", "application/json")
	resp, err := checkClient.Do(req)
	if err != nil {
		return CheckUnreachable, nil
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		endpoint.rejectsWith400 && resp.StatusCode == http.StatusBadRequest:
		if endpoint.regional {
			return CheckUnverified, nil
		}
		return CheckInvalid, nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return CheckUnreachable, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCheckBody))
	if err != nil {
		return CheckUnreachable, nil
	}
	return CheckValid, body
}

// modelIDs reads the OpenAI-style {"data":[{"id"}]} and Gemini-style
// {"models":[{"name"}]} shapes. An unexpected shape lists no models; the key
// was still accepted.
func modelIDs(body []byte, prefix string) []string {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &list) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimPrefix(strings.TrimSpace(id), prefix)
		if id == "" || len(id) > 256 || seen[id] || strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, item := range list.Data {
		add(item.ID)
	}
	for _, item := range list.Models {
		add(item.Name)
	}
	sort.Strings(out)
	if len(out) > maxCheckModels {
		out = out[:maxCheckModels]
	}
	return out
}
