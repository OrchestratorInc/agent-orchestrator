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

const (
	CheckValid       = "valid"
	CheckInvalid     = "invalid"
	CheckUnverified  = "unverified"
	CheckUnreachable = "unreachable"
)

type CheckResult struct {
	Status string   `json:"status" enum:"valid,invalid,unverified,unreachable"`
	Models []string `json:"models,omitempty"`
}

type checkEndpoint struct {
	url            string
	validateURL    string
	authorize      func(*http.Request, string)
	prefix         string
	rejectsWith400 bool
	regional       bool
}

func bearer(req *http.Request, key string) { req.Header.Set("Authorization", "Bearer "+key) }

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
	"MOONSHOT_API_KEY":   {url: "https://api.moonshot.ai/v1/models", authorize: bearer, regional: true},
	"DASHSCOPE_API_KEY":  {url: "https://dashscope.aliyuncs.com/compatible-mode/v1/models", authorize: bearer, regional: true},
}

var checkClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

const (
	maxCheckBody   = 32 << 20
	maxCheckModels = 1000
)

func Checkable(name string) bool {
	_, ok := checkEndpoints[name]
	return ok
}

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
