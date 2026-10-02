package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const credentialPath = "/ao/internal/credentials" // #nosec G101 -- Public route path, not a credential.

const signInTokenPrefix = "sk-ant-oat" // #nosec G101 -- Public format prefix, not a credential.

type credentialHTTP struct {
	key     string
	runtime *credentialRuntime
}

type credentialRecord struct {
	SupportsQuota      bool      `json:"supports_quota"`
	Verification       string    `json:"verification"`
	VerifiedAt         time.Time `json:"verified_at"`
	Generation         uint64    `json:"generation"`
	ReconnectSupported bool      `json:"reconnect_supported"`
	AuthIndex          string    `json:"auth_index"`
	Provider           string    `json:"provider"`
	AccountType        string    `json:"account_type"`
	Label              string    `json:"label"`
	Email              string    `json:"email,omitempty"`
	Status             string    `json:"status"`
	Disabled           bool      `json:"disabled"`
	Unavailable        bool      `json:"unavailable"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	LastRefresh        time.Time `json:"last_refresh"`
}

type credentialCreate struct {
	OperationID string          `json:"operationId"`
	Provider    string          `json:"provider"`
	Key         string          `json:"key,omitempty"`
	BaseURL     string          `json:"baseUrl,omitempty"`
	Credential  json.RawMessage `json:"credential,omitempty"`
}

func (h *credentialHTTP) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validControlAuthorization(request.Header.Get("Authorization"), h.key) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := request.Context()
	path := request.URL.Path
	if request.Method == http.MethodGet && path == credentialPath {
		auths, err := h.runtime.vault.List(ctx)
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		records := make([]credentialRecord, 0, len(auths))
		for _, auth := range auths {
			record := h.record(ctx, auth)
			records = append(records, record)
		}
		writeCredentialJSON(w, map[string]any{"files": records})
		return
	}
	if request.Method == http.MethodPost && (path == credentialPath+"/api-key" || path == credentialPath+"/import") {
		var input credentialCreate
		if decodeCredentialJSON(request, &input) != nil || !validVaultID(input.OperationID) || !validVaultProvider(input.Provider) {
			writeCredentialError(w, errCredentialConflict)
			return
		}
		auth, err := parseCredentialCreate(input, path == credentialPath+"/api-key")
		if err == nil {
			err = h.runtime.vault.Begin(ctx, input.OperationID, input.Provider, time.Now().Add(time.Minute))
			if errors.Is(err, errCredentialCommitted) {
				var prior *coreauth.Auth
				prior, err = h.runtime.vault.Commit(ctx, input.OperationID, auth)
				if err == nil {
					state, _ := h.runtime.vault.verification(ctx, prior)
					if state == "verified" {
						writeCredentialJSON(w, h.record(ctx, prior))
						return
					}
				}
			}
			if err == nil || errors.Is(err, errCredentialCommitted) {
				err = h.runtime.verifyCredential(ctx, auth)
				if err == nil {
					auth, err = h.runtime.completeObserved(ctx, input.OperationID, auth, false, true)
				}
			}
		}
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		writeCredentialJSON(w, h.record(ctx, auth))
		return
	}
	ref := request.URL.Query().Get("ref")
	if ref == "" || len(ref) > 128 {
		writeCredentialError(w, errCredentialConflict)
		return
	}
	auth, resolveErr := h.byRef(ctx, ref)
	if resolveErr != nil {
		writeCredentialError(w, resolveErr)
		return
	}
	if auth == nil {
		if request.Method == http.MethodDelete && path == credentialPath {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	var err error
	switch {
	case request.Method == http.MethodPatch && path == credentialPath+"/label":
		var input struct {
			Label      *string `json:"label"`
			Generation uint64  `json:"generation"`
		}
		if decodeCredentialJSON(request, &input) != nil || input.Label == nil {
			err = errCredentialConflict
		} else {
			err = h.runtime.SetLabel(ctx, auth.ID, *input.Label, input.Generation)
		}
	case request.Method == http.MethodDelete && path == credentialPath:
		err = h.runtime.Remove(ctx, auth.ID)
	case request.Method == http.MethodPatch && path == credentialPath+"/status":
		var input struct {
			Disabled *bool `json:"disabled"`
		}
		if decodeCredentialJSON(request, &input) != nil || input.Disabled == nil {
			err = errCredentialConflict
		} else {
			err = h.runtime.SetEnabled(ctx, auth.ID, !*input.Disabled)
		}
	case request.Method == http.MethodPost && path == credentialPath+"/refresh":
		if auth.Attributes["api_key"] == "" {
			if token, _ := auth.Metadata["refresh_token"].(string); token == "" {
				auth, err = h.runtime.recheckCredential(ctx, auth)
			} else {
				auth, err = h.runtime.Refresh(ctx, auth.ID)
			}
		} else {
			auth, err = h.runtime.recheckCredential(ctx, auth)
		}
		if err == nil {
			writeCredentialJSON(w, h.record(ctx, auth))
			return
		}
	case request.Method == http.MethodGet && path == credentialPath+"/models":
		models := cliproxy.GlobalModelRegistry().GetModelsForClient(auth.ID)
		if models == nil {
			models = []*cliproxy.ModelInfo{}
		}
		writeCredentialJSON(w, map[string]any{"models": models})
		return
	case request.Method == http.MethodGet && path == credentialPath+"/quota":
		if !supportsCredentialQuota(auth) {
			writeRouteError(w, http.StatusNotImplemented, "quota_unavailable")
			return
		}
		quota, quotaErr := h.runtime.quota(ctx, auth)
		if quotaErr != nil {
			writeCredentialQuotaError(w, quotaErr)
			return
		}
		writeCredentialJSON(w, quota)
		return
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err != nil {
		writeCredentialError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *credentialHTTP) byRef(ctx context.Context, ref string) (*coreauth.Auth, error) {
	auths, err := h.runtime.vault.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, auth := range auths {
		if auth.Index == ref {
			return auth, nil
		}
	}
	return nil, nil
}

func (h *credentialHTTP) record(ctx context.Context, auth *coreauth.Auth) credentialRecord {
	record := credentialRecord{
		AuthIndex: auth.Index, Provider: auth.Provider, AccountType: "oauth", Label: auth.Label,
		Status: string(auth.Status), Disabled: auth.Disabled,
		CreatedAt: auth.CreatedAt, UpdatedAt: auth.UpdatedAt, LastRefresh: auth.LastRefreshedAt,
	}
	record.Generation, record.ReconnectSupported = h.runtime.vault.reconnectVersion(ctx, auth)
	record.Verification, record.VerifiedAt = h.runtime.vault.verification(ctx, auth)
	record.SupportsQuota = supportsCredentialQuota(auth)
	if auth.Attributes["api_key"] != "" {
		record.AccountType = "api_key"
		if strings.HasPrefix(auth.Attributes["api_key"], signInTokenPrefix) {
			record.AccountType = "access_token"
		}
	}
	if email, ok := auth.Metadata["email"].(string); ok && len(email) <= 320 {
		record.Email = email
	}
	if !auth.Disabled {
		live, exists := h.runtime.manager.GetByID(auth.ID)
		if !exists || !h.runtime.vault.admitVerified(ctx, live) {
			record.Unavailable, record.Status = true, "error"
		} else {
			record.Unavailable, record.Status = live.Unavailable, string(live.Status)
		}
	}
	return record
}

func parseCredentialCreate(input credentialCreate, apiKey bool) (*coreauth.Auth, error) {
	auth := &coreauth.Auth{Provider: input.Provider, Status: coreauth.StatusActive, Metadata: map[string]any{}, Attributes: map[string]string{}}
	if apiKey {
		key := strings.TrimSpace(input.Key)
		if key == "" || len(key)+len(input.BaseURL) > 16<<10 || len(input.Credential) != 0 || strings.IndexFunc(input.Key, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return nil, errCredentialConflict
		}
		if strings.HasPrefix(key, signInTokenPrefix) {
			return nil, errCredentialInvalid
		}
		base, err := credentialBaseURL(input.Provider, input.BaseURL)
		if err != nil {
			return nil, err
		}
		auth.Attributes["api_key"], auth.Attributes["base_url"] = key, base
		return auth, nil
	}
	if input.Key != "" || input.BaseURL != "" || len(input.Credential) == 0 {
		return nil, errCredentialConflict
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(input.Credential, &metadata) != nil || metadata == nil {
		return nil, errCredentialConflict
	}
	for key, raw := range metadata {
		switch key {
		case "type", "access_token", "refresh_token", "id_token", "account_id", "email", "expired", "last_refresh", "organization_id", "account_uuid", "organization_uuid", "organization_name":
			var value string
			if json.Unmarshal(raw, &value) != nil || len(value) > 64<<10 {
				return nil, errCredentialConflict
			}
			auth.Metadata[key] = value
		case "disabled":
			if json.Unmarshal(raw, &auth.Disabled) != nil {
				return nil, errCredentialConflict
			}
			if auth.Disabled {
				auth.Status = coreauth.StatusDisabled
			}
			auth.Metadata[key] = auth.Disabled
		case "claude_device_ids":
			var values []string
			if input.Provider != "claude" || json.Unmarshal(raw, &values) != nil || len(values) > 1 {
				return nil, errCredentialConflict
			}
			for _, value := range values {
				if value == "" || len(value) > 256 || strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
					return nil, errCredentialConflict
				}
			}
			auth.Metadata[key] = values
		default:
			return nil, errCredentialConflict
		}
	}
	if auth.Metadata["type"] != input.Provider {
		return nil, errCredentialConflict
	}
	if token, _ := auth.Metadata["access_token"].(string); strings.TrimSpace(token) == "" {
		return nil, errCredentialConflict
	}
	return auth, nil
}

func credentialBaseURL(provider, raw string) (string, error) {
	base := strings.TrimSpace(raw)
	if base == "" {
		if provider == "codex" {
			base = "https://api.openai.com/v1"
		} else {
			base = "https://api.anthropic.com"
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errCredentialConflict
	}
	parsed.Scheme, parsed.Host = strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Host)
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return "", errCredentialConflict
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func decodeCredentialJSON(request *http.Request, output any) error {
	body, err := io.ReadAll(io.LimitReader(request.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return errCredentialConflict
	}
	defer clear(body)
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return errCredentialConflict
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errCredentialConflict
	}
	return nil
}

func writeCredentialJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func writeCredentialError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "credential_storage_unavailable"
	if errors.Is(err, errCredentialConflict) {
		status, code = http.StatusConflict, "credential_conflict"
	}
	if errors.Is(err, errCredentialFenced) {
		status, code = http.StatusConflict, "credential_unavailable"
	}
	if errors.Is(err, errCredentialInvalid) {
		status, code = http.StatusUnprocessableEntity, "credential_invalid"
	}
	if errors.Is(err, errCredentialCheckUnavailable) {
		status, code = http.StatusFailedDependency, "credential_verification_unavailable"
	}
	writeRouteError(w, status, code)
}
