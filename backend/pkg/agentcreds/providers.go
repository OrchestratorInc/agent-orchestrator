package agentcreds

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Provider string

const (
	ProviderFirstParty Provider = "firstParty"
	ProviderGateway    Provider = "gateway"
	ProviderFoundry    Provider = "foundry"
	ProviderBedrock    Provider = "bedrock"
	ProviderVertex     Provider = "vertex"
)

func ParseProvider(value string) (Provider, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "firstparty":
		return ProviderFirstParty, true
	case "gateway":
		return ProviderGateway, true
	case "foundry":
		return ProviderFoundry, true
	case "bedrock":
		return ProviderBedrock, true
	case "vertex":
		return ProviderVertex, true
	default:
		return "", false
	}
}

func (v *Validator) requestFor(ctx context.Context, provider Provider, cred Credential) (requestSpec, error) {
	switch provider {
	case ProviderFirstParty, ProviderGateway:
		return v.anthropicRequest(ctx, provider, cred)
	default:
		return requestSpec{}, fmt.Errorf("agentcreds: unsupported provider %q", provider)
	}
}

func (v *Validator) anthropicRequest(ctx context.Context, provider Provider, cred Credential) (requestSpec, error) {
	base := firstNonEmpty(cred.BaseURL, "https://api.anthropic.com")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/models", http.NoBody)
	if err != nil {
		return requestSpec{}, err
	}
	request.Header.Set("anthropic-version", anthropicAPIVersion)
	query := request.URL.Query()
	query.Set("limit", "1000")
	request.URL.RawQuery = query.Encode()
	if err := setAnthropicAuth(request, cred); err != nil {
		return requestSpec{}, err
	}
	label := "Anthropic"
	parseModels := parseAnthropicModels
	if provider == ProviderGateway {
		label = "the configured gateway"
		parseModels = parseGatewayModels
	}
	return requestSpec{
		request:                       request,
		parseModels:                   parseModels,
		rateLimitProvesAuthentication: provider == ProviderFirstParty,
		paginateAnthropic:             true,
		label:                         label,
	}, nil
}

func setAnthropicAuth(request *http.Request, cred Credential) error {
	secret := strings.TrimSpace(cred.Secret)
	if secret == "" {
		return fmt.Errorf("agentcreds: no secret for %s credential from %s", cred.Kind, cred.Source)
	}
	switch cred.Kind {
	case KindAPIKey:
		request.Header.Set("x-api-key", secret)
	case KindOAuthToken:
		request.Header.Set("authorization", "Bearer "+secret)
		request.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
		request.Header.Set("x-app", "cli")
		request.Header.Set("user-agent", "claude-code/2.1.220")
	case KindAuthToken:
		request.Header.Set("authorization", "Bearer "+secret)
	default:
		return fmt.Errorf("agentcreds: credential kind %q cannot authenticate to Anthropic", cred.Kind)
	}
	return nil
}

func parseAnthropicModels(body []byte) ([]Model, error) {
	return parseAnthropicCompatibleModels(body, true)
}

func parseGatewayModels(body []byte) ([]Model, error) {
	return parseAnthropicCompatibleModels(body, false)
}

func parseAnthropicCompatibleModels(body []byte, claudeOnly bool) ([]Model, error) {
	var payload struct {
		Data []struct {
			ID           string `json:"id"`
			DisplayName  string `json:"display_name"`
			Capabilities struct {
				Effort map[string]json.RawMessage `json:"effort"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(payload.Data))
	for _, entry := range payload.Data {
		if strings.TrimSpace(entry.ID) == "" || claudeOnly && !isClaudeModelID(entry.ID) {
			continue
		}
		models = append(models, Model{
			ID:          entry.ID,
			DisplayName: strings.TrimSpace(entry.DisplayName),
			Efforts:     supportedEfforts(entry.Capabilities.Effort),
		})
	}
	return models, nil
}

func anthropicPageCursor(body []byte) (bool, string, error) {
	var payload struct {
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false, "", err
	}
	return payload.HasMore, strings.TrimSpace(payload.LastID), nil
}

var effortOrder = []string{"low", "medium", "high", "xhigh", "max"}

func supportedEfforts(raw map[string]json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var supported bool
	if value, ok := raw["supported"]; ok {
		if json.Unmarshal(value, &supported) != nil || !supported {
			return nil
		}
	}
	efforts := make([]string, 0, len(effortOrder))
	for _, level := range effortOrder {
		value, ok := raw[level]
		if !ok {
			continue
		}
		var entry struct {
			Supported bool `json:"supported"`
		}
		if json.Unmarshal(value, &entry) == nil && entry.Supported {
			efforts = append(efforts, level)
		}
	}
	return efforts
}

func isClaudeModelID(id string) bool {
	return strings.Contains(strings.ToLower(id), "claude")
}

func providerErrorMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for _, candidate := range []string{payload.Error.Message, payload.Message, payload.Error.Code} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
