package accountsmanager

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultCodexBaseURL  = "https://api.openai.com/v1"
	defaultClaudeBaseURL = "https://api.anthropic.com"
)

// APIKeyInput is secret-bearing input and must never enter a public response or log.
type APIKeyInput struct {
	OperationID string
	Provider    Provider
	Key         string
	BaseURL     string
}

// CredentialImport holds caller-selected credential data before bounded validation.
type CredentialImport struct {
	OperationID string
	Provider    Provider
	Name        string
	JSON        json.RawMessage
}

type rawCredentialRecord struct {
	Verification       string                         `json:"verification"`
	VerifiedAt         time.Time                      `json:"verified_at"`
	Label              string                         `json:"label"`
	Generation         uint64                         `json:"generation"`
	ReconnectSupported bool                           `json:"reconnect_supported"`
	AuthIndex          string                         `json:"auth_index"`
	Name               string                         `json:"name"`
	Provider           string                         `json:"provider"`
	Type               string                         `json:"type"`
	AccountType        string                         `json:"account_type"`
	Email              string                         `json:"email"`
	Status             string                         `json:"status"`
	Disabled           bool                           `json:"disabled"`
	Unavailable        bool                           `json:"unavailable"`
	CreatedAt          time.Time                      `json:"created_at"`
	UpdatedAt          time.Time                      `json:"updated_at"`
	LastRefresh        time.Time                      `json:"last_refresh"`
	SupportsQuota      bool                           `json:"supports_quota"`
	Cooldowns          []CredentialCooldown           `json:"cooldowns"`
	Quota              rawQuotaObservation            `json:"quota"`
	ModelQuota         map[string]rawQuotaObservation `json:"model_quotas"`
}

type rawQuotaObservation struct {
	ObservedAt time.Time         `json:"observed_at"`
	Signals    map[string]string `json:"signals"`
}

// AddAPIKey commits one encrypted credential through the runner-owned writer.
func (c *ManagementClient) AddAPIKey(ctx context.Context, input APIKeyInput) (CredentialSummary, error) {
	if !validProvider(input.Provider) {
		return CredentialSummary{}, ErrUnsupportedProvider
	}
	key := strings.TrimSpace(input.Key)
	if key == "" {
		return CredentialSummary{}, ErrInvalidCredential
	}
	if len(key)+len(input.BaseURL) > managementAPIKeyLimit {
		return CredentialSummary{}, ErrRequestTooLarge
	}
	if strings.HasPrefix(key, "sk-ant-oat") {
		return CredentialSummary{}, ErrCredentialMethod
	}
	baseURL, err := normalizeProviderBaseURL(input.Provider, input.BaseURL)
	if err != nil {
		return CredentialSummary{}, err
	}
	operationID := strings.TrimSpace(input.OperationID)
	if operationID == "" {
		operationID = rand.Text()
	}
	return c.createCredential(ctx, "api-key", input.Provider, map[string]any{
		"operationId": operationID, "provider": input.Provider, "key": key, "baseUrl": baseURL,
	})
}

// ImportCredential transmits only the explicitly supplied bounded document.
func (c *ManagementClient) ImportCredential(ctx context.Context, input CredentialImport) (CredentialSummary, error) {
	if !validProvider(input.Provider) {
		return CredentialSummary{}, ErrUnsupportedProvider
	}
	if len(input.JSON) == 0 {
		return CredentialSummary{}, ErrInvalidCredential
	}
	if len(input.JSON) > managementImportLimit {
		return CredentialSummary{}, ErrRequestTooLarge
	}
	if _, err := validateCredentialImport(input); err != nil {
		return CredentialSummary{}, err
	}
	operationID := strings.TrimSpace(input.OperationID)
	if operationID == "" {
		operationID = rand.Text()
	}
	return c.createCredential(ctx, "import", input.Provider, map[string]any{
		"operationId": operationID, "provider": input.Provider, "credential": input.JSON,
	})
}

func (c *ManagementClient) createCredential(ctx context.Context, method string, provider Provider, input any) (CredentialSummary, error) {
	var record rawCredentialRecord
	if err := c.doJSON(ctx, "create credential", http.MethodPost, credentialManagementPath+"/"+method, input, &record); err != nil {
		return CredentialSummary{}, mapCredentialOperationError(err)
	}
	summary := summaryFromRawCredential(record)
	if summary.Ref == "" || summary.Provider != provider || summary.Kind == CredentialUnknown {
		return CredentialSummary{}, ErrInvalidResponse
	}
	return summary, nil
}

func normalizeProviderBaseURL(provider Provider, raw string) (string, error) {
	base := strings.TrimSpace(raw)
	if base == "" {
		if provider == ProviderCodex {
			base = defaultCodexBaseURL
		} else {
			base = defaultClaudeBaseURL
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrInvalidCredential
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		loopback := strings.EqualFold(parsed.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
		if parsed.Scheme != "http" || !loopback {
			return "", ErrInvalidCredential
		}
	}
	if parsed.Path != "/" {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	return parsed.String(), nil
}

func validateCredentialImport(input CredentialImport) (string, error) {
	name := strings.TrimSpace(input.Name)
	lower := strings.ToLower(name)
	if name == "" || len(name) > 255 || filepath.Base(name) != name || strings.HasPrefix(name, ".") || !strings.HasSuffix(lower, ".json") || strings.Contains(lower, "quota_probe") || strings.HasPrefix(lower, ".oauth-") {
		return "", ErrInvalidCredential
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '.' || character == '-' || character == '_' {
			continue
		}
		return "", ErrInvalidCredential
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(input.JSON, &object) != nil || object == nil {
		return "", ErrInvalidCredential
	}
	var documentType string
	if rawType, ok := object["type"]; !ok || json.Unmarshal(rawType, &documentType) != nil || Provider(strings.ToLower(strings.TrimSpace(documentType))) != input.Provider {
		return "", ErrInvalidCredential
	}
	if rawProbe, ok := object["quota_probe"]; ok && len(rawProbe) > 0 && string(rawProbe) != "null" {
		return "", ErrInvalidCredential
	}
	return name, nil
}

func (c *ManagementClient) listRawCredentials(ctx context.Context) ([]rawCredentialRecord, error) {
	var payload struct {
		Files []rawCredentialRecord `json:"files"`
	}
	if err := c.doJSON(ctx, "list credentials", http.MethodGet, credentialManagementPath, nil, &payload); err != nil {
		return nil, err
	}
	return payload.Files, nil
}

func summaryFromRawCredential(record rawCredentialRecord) CredentialSummary {
	if record.Verification != "verified" && record.Verification != "invalid" && record.Verification != "unverified" {
		record.Verification = "unverified"
	}
	if record.Verification == "verified" && record.VerifiedAt.IsZero() {
		record.Verification = "unverified"
	}
	if record.Verification != "verified" {
		record.Unavailable = true
		record.VerifiedAt = time.Time{}
	}
	provider := Provider(strings.ToLower(strings.TrimSpace(record.Provider)))
	if provider == "" {
		provider = Provider(strings.ToLower(strings.TrimSpace(record.Type)))
	}
	kind := normalizeCredentialKind(record.AccountType)
	return CredentialSummary{
		Verification: record.Verification, VerifiedAt: record.VerifiedAt,
		Label: record.Label, Generation: record.Generation, ReconnectSupported: record.ReconnectSupported,
		Ref:             strings.TrimSpace(record.AuthIndex),
		Provider:        provider,
		Kind:            kind,
		Email:           strings.TrimSpace(record.Email),
		Status:          normalizeCredentialState(record.Status, record.Disabled),
		Disabled:        record.Disabled,
		Unavailable:     record.Unavailable,
		CreatedAt:       record.CreatedAt,
		UpdatedAt:       record.UpdatedAt,
		LastRefreshedAt: record.LastRefresh,
		QuotaSupported:  record.SupportsQuota && kind != CredentialAccessToken,
		Cooldowns:       append([]CredentialCooldown(nil), record.Cooldowns...),
		Quota:           projectQuotaObservation(record.Quota),
		ModelQuota:      projectModelQuotaObservations(record.ModelQuota),
	}
}

func projectQuotaObservation(raw rawQuotaObservation) CredentialQuotaObservation {
	if len(raw.Signals) == 0 {
		return CredentialQuotaObservation{ObservedAt: raw.ObservedAt}
	}
	signals := make(map[string]string, len(raw.Signals))
	for key, value := range raw.Signals {
		signals[key] = value
	}
	return CredentialQuotaObservation{ObservedAt: raw.ObservedAt, Signals: signals}
}

func projectModelQuotaObservations(raw map[string]rawQuotaObservation) map[string]CredentialQuotaObservation {
	if len(raw) == 0 {
		return nil
	}
	projected := make(map[string]CredentialQuotaObservation, len(raw))
	for model, observation := range raw {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			projected[trimmed] = projectQuotaObservation(observation)
		}
	}
	return projected
}

func normalizeCredentialKind(raw string) CredentialKind {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "oauth":
		return CredentialOAuth
	case "api_key", "api-key", "apikey":
		return CredentialAPIKey
	case "access_token":
		return CredentialAccessToken
	default:
		return CredentialUnknown
	}
}

func normalizeCredentialState(raw string, disabled bool) CredentialState {
	if disabled {
		return CredentialDisabled
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "active", "ok":
		return CredentialActive
	case "pending":
		return CredentialPending
	case "refreshing", "recovering":
		return CredentialRefreshing
	case "error", "failed", "unavailable":
		return CredentialError
	case "disabled":
		return CredentialDisabled
	default:
		return CredentialUnknownState
	}
}
