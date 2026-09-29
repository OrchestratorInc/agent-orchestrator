package accountsmanager

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CredentialModel is a model advertised for one runner credential.
type CredentialModel struct {
	ID          string
	DisplayName string
	Type        string
	Owner       string
	Efforts     []string
}

// QuotaSubscription carries provider-reported plan information, not local billing state.
type QuotaSubscription struct {
	Plan     string `json:"plan"`
	TierName string `json:"tierName"`
	TierID   string `json:"tierId"`
}

// QuotaMetric retains the provider's units and format for a usage measurement.
type QuotaMetric struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit"`
	Format   string  `json:"format"`
	Currency string  `json:"currency"`
}

// QuotaBucket describes one provider-reported quota window.
type QuotaBucket struct {
	Window            string  `json:"window"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
	Description       string  `json:"description"`
}

// QuotaGroup groups quota windows with a shared provider label.
type QuotaGroup struct {
	DisplayName string        `json:"displayName"`
	Buckets     []QuotaBucket `json:"buckets"`
}

// CredentialQuota contains validated quota observations without credential material.
type CredentialQuota struct {
	ObservedAt         time.Time          `json:"observedAt"`
	Subscription       *QuotaSubscription `json:"subscription"`
	Summary            []QuotaMetric      `json:"summary"`
	ServerTimeOffsetMS int64              `json:"serverTimeOffsetMs"`
	Groups             []QuotaGroup       `json:"groups"`
}

// QuotaError keeps provider bodies and transport details out of public errors.
type QuotaError struct{ StatusCode int }

func (*QuotaError) Error() string { return "account usage lookup failed" }

func (e *QuotaError) Unwrap() error {
	if e.StatusCode == http.StatusBadGateway {
		return ErrInvalidResponse
	}
	return nil
}

// ListCredentialModels requires an unambiguous credential with a backing auth record.
func (c *ManagementClient) ListCredentialModels(ctx context.Context, ref string) ([]CredentialModel, error) {
	record, err := c.resolveCredential(ctx, ref)
	if err != nil {
		return nil, err
	}
	query := url.Values{"ref": []string{record.AuthIndex}}
	var response struct {
		Models []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Type        string `json:"type"`
			Owner       string `json:"owned_by"`
			Thinking    *struct {
				Levels []string `json:"levels"`
			} `json:"thinking"`
		} `json:"models"`
	}
	if err = c.doJSON(ctx, "list credential models", http.MethodGet, credentialManagementPath+"/models?"+query.Encode(), nil, &response); err != nil {
		return nil, mapCredentialOperationError(err)
	}
	if len(response.Models) > 4096 {
		return nil, ErrInvalidResponse
	}
	models := make([]CredentialModel, 0, len(response.Models))
	seen := make(map[string]struct{}, len(response.Models))
	for _, raw := range response.Models {
		id := strings.TrimSpace(raw.ID)
		if id == "" || len(id) > 512 {
			return nil, ErrInvalidResponse
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		var efforts []string
		if raw.Thinking != nil {
			if len(raw.Thinking.Levels) > 16 {
				return nil, ErrInvalidResponse
			}
			for _, effort := range raw.Thinking.Levels {
				switch effort {
				case "none", "minimal", "low", "medium", "high", "xhigh", "max", "auto":
					efforts = append(efforts, effort)
				default:
					return nil, ErrInvalidResponse
				}
			}
		}
		models = append(models, CredentialModel{
			ID:          id,
			DisplayName: strings.TrimSpace(raw.DisplayName),
			Type:        strings.TrimSpace(raw.Type),
			Owner:       strings.TrimSpace(raw.Owner),
			Efforts:     efforts,
		})
	}
	return models, nil
}

// FetchCredentialQuota rejects credentials whose provider does not support quota checks.
func (c *ManagementClient) FetchCredentialQuota(ctx context.Context, ref string) (CredentialQuota, error) {
	record, err := c.resolveCredential(ctx, ref)
	if err != nil {
		return CredentialQuota{}, mapCredentialOperationError(err)
	}
	if !record.SupportsQuota || normalizeCredentialKind(record.AccountType) == CredentialAccessToken {
		return CredentialQuota{}, ErrOperationUnsupported
	}
	var quota CredentialQuota
	err = c.doJSON(ctx, "fetch credential quota", http.MethodGet, credentialManagementPath+"/quota?"+url.Values{"ref": {record.AuthIndex}}.Encode(), nil, &quota)
	if err != nil {
		var statusErr *ManagementStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotImplemented {
			return CredentialQuota{}, ErrOperationUnsupported
		}
		status := http.StatusServiceUnavailable
		if errors.As(err, &statusErr) {
			switch statusErr.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway:
				status = statusErr.StatusCode
			case http.StatusConflict, http.StatusNotFound:
				return CredentialQuota{}, mapCredentialOperationError(err)
			}
		} else if errors.Is(err, ErrInvalidResponse) || errors.Is(err, ErrResponseTooLarge) {
			status = http.StatusBadGateway
		}
		return CredentialQuota{}, &QuotaError{StatusCode: status}
	}
	if !validCredentialQuota(quota) {
		return CredentialQuota{}, &QuotaError{StatusCode: http.StatusBadGateway}
	}
	return quota, nil
}

// ResetCredentialQuota requires the provider to explicitly advertise reset support.
func (c *ManagementClient) ResetCredentialQuota(ctx context.Context, ref string) error {
	c.mutationMu.Lock()
	defer c.mutationMu.Unlock()
	record, err := c.resolveCredential(ctx, ref)
	if err != nil {
		return err
	}
	if !record.SupportsQuota || normalizeCredentialKind(record.AccountType) == CredentialAccessToken {
		return ErrOperationUnsupported
	}
	provider := Provider(strings.ToLower(strings.TrimSpace(record.Provider)))
	if provider == "" {
		provider = Provider(strings.ToLower(strings.TrimSpace(record.Type)))
	}
	var providers struct {
		Providers []struct {
			Provider           string   `json:"provider"`
			SupportedProviders []string `json:"supported_providers"`
			SupportsReset      bool     `json:"supports_reset"`
		} `json:"providers"`
	}
	if err := c.doJSON(ctx, "list quota providers", http.MethodGet, "/v0/management/quota/providers", nil, &providers); err != nil {
		return err
	}
	resetSupported := false
	for _, candidate := range providers.Providers {
		if !candidate.SupportsReset {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(candidate.Provider), string(provider)) {
			resetSupported = true
			break
		}
		for _, supported := range candidate.SupportedProviders {
			if strings.EqualFold(strings.TrimSpace(supported), string(provider)) {
				resetSupported = true
				break
			}
		}
		if resetSupported {
			break
		}
	}
	if !resetSupported {
		return ErrOperationUnsupported
	}
	var response map[string]any
	err = c.doJSON(ctx, "reset credential quota", http.MethodPost, "/v0/management/quota/reset", map[string]string{"auth_index": record.AuthIndex}, &response)
	var statusErr *ManagementStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotImplemented {
		return ErrOperationUnsupported
	}
	return err
}

func validCredentialQuota(quota CredentialQuota) bool {
	if quota.ObservedAt.IsZero() || len(quota.Summary) > 256 || len(quota.Groups) == 0 || len(quota.Groups) > 128 {
		return false
	}
	for _, metric := range quota.Summary {
		if strings.TrimSpace(metric.Key) == "" || len(metric.Key) > 256 || len(metric.Label) > 512 {
			return false
		}
	}
	for _, group := range quota.Groups {
		if len(group.DisplayName) > 512 || len(group.Buckets) == 0 || len(group.Buckets) > 256 {
			return false
		}
		for _, bucket := range group.Buckets {
			if bucket.Window == "" || len(bucket.Window) > 256 || len(bucket.Description) > 512 || bucket.RemainingFraction < 0 || bucket.RemainingFraction > 1 {
				return false
			}
			if bucket.ResetTime != "" {
				if _, err := time.Parse(time.RFC3339, bucket.ResetTime); err != nil {
					return false
				}
			}
		}
	}
	return true
}
