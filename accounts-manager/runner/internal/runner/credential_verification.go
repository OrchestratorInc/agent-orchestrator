package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

var (
	errCredentialInvalid          = errors.New("provider rejected credential")
	errCredentialCheckUnavailable = errors.New("credential verification is unavailable")
)

const credentialCheckLimit = 1 << 20

type credentialCheckStatusError struct{ status int }

func (e *credentialCheckStatusError) Error() string { return e.Unwrap().Error() }

func (e *credentialCheckStatusError) Unwrap() error {
	if e.status == http.StatusUnauthorized {
		return errCredentialInvalid
	}
	return errCredentialCheckUnavailable
}

func (r *credentialRuntime) verifyCredential(ctx context.Context, auth *coreauth.Auth) error {
	if auth == nil || !validVaultProvider(auth.Provider) {
		return errCredentialConflict
	}
	key := auth.Attributes["api_key"]
	if key == "" {
		return r.verifyImportedCredential(ctx, auth)
	}
	base, err := credentialBaseURL(auth.Provider, auth.Attributes["base_url"])
	if err != nil {
		return err
	}
	if auth.Provider != "codex" && !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	headers := make(http.Header)
	if auth.Provider == "codex" {
		headers.Set("Authorization", "Bearer "+key)
	} else {
		headers.Set("anthropic-version", "2023-06-01")
		if strings.HasPrefix(key, "sk-ant-oat") {
			headers.Set("Authorization", "Bearer "+key)
			headers.Set("anthropic-beta", "oauth-2025-04-20")
		} else {
			headers.Set("x-api-key", key)
		}
	}
	data, err := r.credentialCheck(ctx, base+"/models", headers)
	if err != nil {
		return err
	}
	defer clear(data)
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Data) == 0 || len(result.Data) > 4096 {
		return errCredentialCheckUnavailable
	}
	for _, model := range result.Data {
		if strings.TrimSpace(model.ID) == "" || len(model.ID) > 512 {
			return errCredentialCheckUnavailable
		}
	}
	return nil
}

func (r *credentialRuntime) verifyImportedCredential(ctx context.Context, auth *coreauth.Auth) error {
	token, _ := auth.Metadata["access_token"].(string)
	if token == "" || strings.IndexFunc(token, func(c rune) bool { return c < 32 || c == 127 }) >= 0 {
		return errCredentialConflict
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	endpoint := "https://api.anthropic.com/api/oauth/profile"
	if auth.Provider == "codex" {
		endpoint = "https://chatgpt.com/backend-api/wham/usage"
		if account, _ := auth.Metadata["account_id"].(string); account != "" {
			if !validIdentityAtom(account) {
				return errCredentialConflict
			}
			headers.Set("ChatGPT-Account-Id", account)
		}
	} else {
		headers.Set("anthropic-beta", "oauth-2025-04-20")
	}
	data, err := r.credentialCheck(ctx, endpoint, headers)
	if err != nil {
		return err
	}
	defer clear(data)
	var result struct {
		RateLimit *json.RawMessage `json:"rate_limit"`
		Account   struct {
			UUID string `json:"uuid"`
		} `json:"account"`
		Organization struct {
			UUID string `json:"uuid"`
		} `json:"organization"`
	}
	if json.Unmarshal(data, &result) != nil {
		return errCredentialCheckUnavailable
	}
	if auth.Provider == "codex" {
		if result.RateLimit == nil || len(*result.RateLimit) == 0 || (*result.RateLimit)[0] != '{' {
			return errCredentialCheckUnavailable
		}
	} else if !validIdentityAtom(result.Account.UUID) || !validIdentityAtom(result.Organization.UUID) {
		return errCredentialCheckUnavailable
	}
	return nil
}

func (r *credentialRuntime) credentialCheck(ctx context.Context, endpoint string, headers http.Header) (data []byte, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if r.context != nil {
		stop := context.AfterFunc(r.context, cancel)
		defer stop()
	}
	r.checkMu.Lock()
	if r.checkSlots == nil {
		r.checkSlots = make(chan struct{}, 4)
	}
	slots := r.checkSlots
	r.checkMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, errCredentialCheckUnavailable
	case slots <- struct{}{}:
	}
	defer func() { <-slots }()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, errCredentialCheckUnavailable
	}
	request.Header = headers.Clone()
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Transport: r.checkTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, errCredentialCheckUnavailable
	}
	defer func() {
		if err := response.Body.Close(); err != nil && resultErr == nil {
			clear(data)
			data, resultErr = nil, errCredentialCheckUnavailable
		}
	}()
	if response.StatusCode != http.StatusOK {
		return nil, &credentialCheckStatusError{status: response.StatusCode}
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, credentialCheckLimit+1))
	if err != nil || len(data) > credentialCheckLimit {
		clear(data)
		return nil, errCredentialCheckUnavailable
	}
	return data, nil
}

func (r *credentialRuntime) recheckCredential(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	r.checkMu.Lock()
	if r.recheckClosed || !r.vault.matchesCommitted(ctx, auth, false) {
		r.checkMu.Unlock()
		return nil, errCredentialFenced
	}
	ctx, cancel := context.WithCancel(ctx)
	flight := &credentialRecheck{done: make(chan struct{}), cancel: cancel}
	if r.rechecks == nil {
		r.rechecks = make(map[string]map[*credentialRecheck]struct{})
	}
	if r.rechecks[auth.ID] == nil {
		r.rechecks[auth.ID] = make(map[*credentialRecheck]struct{})
	}
	r.rechecks[auth.ID][flight] = struct{}{}
	r.recheckWorkers.Add(1)
	r.checkMu.Unlock()
	defer r.recheckWorkers.Done()
	defer func() {
		cancel()
		r.checkMu.Lock()
		delete(r.rechecks[auth.ID], flight)
		if len(r.rechecks[auth.ID]) == 0 {
			delete(r.rechecks, auth.ID)
		}
		close(flight.done)
		r.checkMu.Unlock()
	}()
	err := r.verifyCredential(ctx, auth)
	if !r.vault.matchesCommitted(ctx, auth, false) {
		return nil, errCredentialFenced
	}
	if err != nil && !errors.Is(err, errCredentialInvalid) {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if saveErr := r.vault.recordVerification(ctx, auth, err == nil); saveErr != nil {
		return nil, saveErr
	}
	if err != nil {
		return nil, err
	}
	return auth, nil
}
