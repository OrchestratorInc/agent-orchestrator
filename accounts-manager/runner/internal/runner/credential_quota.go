package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type credentialQuota struct {
	ObservedAt         time.Time          `json:"observedAt"`
	Subscription       *quotaSubscription `json:"subscription,omitempty"`
	Summary            []any              `json:"summary"`
	ServerTimeOffsetMS int64              `json:"serverTimeOffsetMs"`
	Groups             []quotaGroup       `json:"groups"`
}

type quotaSubscription struct {
	Plan string `json:"plan"`
}
type quotaGroup struct {
	DisplayName string        `json:"displayName"`
	Buckets     []quotaBucket `json:"buckets"`
}
type quotaBucket struct {
	Window            string  `json:"window"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
	Description       string  `json:"description"`
}

type credentialQuotaFlight struct {
	done        chan struct{}
	fingerprint string
	until       time.Time
	result      credentialQuota
	err         error
	context     context.Context
	cancel      context.CancelFunc
	waiters     int
	complete    bool
}

func supportsCredentialQuota(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	token, _ := auth.Metadata["access_token"].(string)
	return validVaultProvider(auth.Provider) && auth.Attributes["api_key"] == "" && token != ""
}

func (r *credentialRuntime) quota(ctx context.Context, auth *coreauth.Auth) (credentialQuota, error) {
	if err := ctx.Err(); err != nil {
		return credentialQuota{}, err
	}
	if !supportsCredentialQuota(auth) || !r.vault.admitVerified(ctx, auth) {
		return credentialQuota{}, errCredentialFenced
	}
	fingerprint, err := credentialFingerprint(auth)
	if err != nil {
		return credentialQuota{}, err
	}
	r.quotaMu.Lock()
	if r.quotaClosed || !r.vault.admitVerified(ctx, auth) {
		r.quotaMu.Unlock()
		return credentialQuota{}, errCredentialFenced
	}
	if r.quotas == nil {
		r.quotas = make(map[string]*credentialQuotaFlight)
		r.quotaPending = make(map[string]map[*credentialQuotaFlight]struct{})
	}
	flight := r.quotas[auth.ID]
	if flight == nil || flight.fingerprint != fingerprint || flight.complete && !time.Now().Before(flight.until) {
		if flight != nil {
			flight.cancel()
		}
		parent := r.context
		if parent == nil {
			parent = context.WithoutCancel(ctx)
		}
		flight = &credentialQuotaFlight{done: make(chan struct{}), fingerprint: fingerprint}
		flight.context, flight.cancel = context.WithTimeout(parent, 3*time.Second)
		r.quotas[auth.ID] = flight
		if r.quotaPending[auth.ID] == nil {
			r.quotaPending[auth.ID] = make(map[*credentialQuotaFlight]struct{})
		}
		r.quotaPending[auth.ID][flight] = struct{}{}
		r.quotaWorkers.Add(1)
		go r.runQuota(auth.Clone(), flight)
	}
	flight.waiters++
	r.quotaMu.Unlock()
	defer r.releaseQuotaWaiter(auth.ID, flight)
	select {
	case <-ctx.Done():
		return credentialQuota{}, ctx.Err()
	case <-flight.done:
	case <-flight.context.Done():
		select {
		case <-flight.done:
		default:
			if err := ctx.Err(); err != nil {
				return credentialQuota{}, err
			}
			if r.vault.admitVerified(ctx, auth) && errors.Is(flight.context.Err(), context.DeadlineExceeded) {
				return credentialQuota{}, errCredentialCheckUnavailable
			}
			return credentialQuota{}, errCredentialFenced
		}
	}
	if err := ctx.Err(); err != nil {
		return credentialQuota{}, err
	}
	if !r.vault.admitVerified(ctx, auth) {
		return credentialQuota{}, errCredentialFenced
	}
	return flight.result, flight.err
}

func (r *credentialRuntime) runQuota(auth *coreauth.Auth, flight *credentialQuotaFlight) {
	defer r.quotaWorkers.Done()
	result, err := r.fetchQuota(flight.context, auth)
	if !r.vault.admitVerified(context.WithoutCancel(flight.context), auth) {
		result, err = credentialQuota{}, errCredentialFenced
	} else if errors.Is(flight.context.Err(), context.DeadlineExceeded) {
		result, err = credentialQuota{}, errCredentialCheckUnavailable
	} else if flight.context.Err() != nil {
		result, err = credentialQuota{}, errCredentialFenced
	}
	r.quotaMu.Lock()
	flight.result, flight.err = result, err
	flight.until = time.Now().Add(30 * time.Second)
	if err != nil {
		flight.until = time.Now().Add(time.Second)
	}
	flight.complete = true
	delete(r.quotaPending[auth.ID], flight)
	if len(r.quotaPending[auth.ID]) == 0 {
		delete(r.quotaPending, auth.ID)
	}
	close(flight.done)
	flight.cancel()
	r.quotaMu.Unlock()
}

func (r *credentialRuntime) releaseQuotaWaiter(id string, flight *credentialQuotaFlight) {
	r.quotaMu.Lock()
	defer r.quotaMu.Unlock()
	flight.waiters--
	if flight.waiters == 0 && !flight.complete {
		if r.quotas[id] == flight {
			delete(r.quotas, id)
		}
		flight.cancel()
	}
}

func (r *credentialRuntime) cancelQuota(id string) {
	r.quotaMu.Lock()
	defer r.quotaMu.Unlock()
	if flight := r.quotas[id]; flight != nil {
		flight.cancel()
		delete(r.quotas, id)
	}
}

func (r *credentialRuntime) stopQuota() {
	r.quotaMu.Lock()
	r.quotaClosed = true
	for _, pending := range r.quotaPending {
		for flight := range pending {
			flight.cancel()
		}
	}
	r.quotaMu.Unlock()
}

func (r *credentialRuntime) fetchQuota(ctx context.Context, auth *coreauth.Auth) (credentialQuota, error) {
	token, _ := auth.Metadata["access_token"].(string)
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	endpoint := "https://api.anthropic.com/api/oauth/usage"
	if auth.Provider == "codex" {
		endpoint = "https://chatgpt.com/backend-api/wham/usage"
		if account, _ := auth.Metadata["account_id"].(string); account != "" {
			if !validIdentityAtom(account) {
				return credentialQuota{}, errCredentialConflict
			}
			headers.Set("ChatGPT-Account-Id", account)
		}
	} else {
		headers.Set("anthropic-beta", "oauth-2025-04-20")
	}
	body, err := r.credentialCheck(ctx, endpoint, headers)
	if err != nil {
		return credentialQuota{}, err
	}
	defer clear(body)
	return parseCredentialQuota(auth.Provider, body, time.Now().UTC())
}

func parseCredentialQuota(provider string, data []byte, observed time.Time) (credentialQuota, error) {
	result := credentialQuota{ObservedAt: observed, Summary: []any{}, Groups: []quotaGroup{{DisplayName: "Account", Buckets: []quotaBucket{}}}}
	add := func(window string, used *float64, reset string) bool {
		if used == nil || *used < 0 || *used > 100 {
			return false
		}
		if reset != "" {
			if _, err := time.Parse(time.RFC3339, reset); err != nil {
				return false
			}
		}
		result.Groups[0].Buckets = append(result.Groups[0].Buckets, quotaBucket{Window: window, RemainingFraction: 1 - *used/100, ResetTime: reset})
		return true
	}
	if provider == "codex" {
		type window struct {
			Used    *float64 `json:"used_percent"`
			Seconds *int64   `json:"limit_window_seconds"`
			Reset   *int64   `json:"reset_at"`
		}
		var raw struct {
			Plan  string `json:"plan_type"`
			Limit *struct {
				Primary   *window `json:"primary_window"`
				Secondary *window `json:"secondary_window"`
			} `json:"rate_limit"`
		}
		if json.Unmarshal(data, &raw) != nil || raw.Limit == nil {
			return credentialQuota{}, errCredentialQuotaResponse
		}
		for index, value := range []*window{raw.Limit.Primary, raw.Limit.Secondary} {
			if value == nil {
				continue
			}
			name := []string{"primary", "secondary"}[index]
			if value.Seconds != nil {
				if *value.Seconds <= 0 {
					return credentialQuota{}, errCredentialQuotaResponse
				}
				name = strconv.FormatInt(*value.Seconds, 10) + "s"
			}
			reset := ""
			if value.Reset != nil {
				if *value.Reset < 0 || *value.Reset > 253402300799 {
					return credentialQuota{}, errCredentialQuotaResponse
				}
				reset = time.Unix(*value.Reset, 0).UTC().Format(time.RFC3339)
			}
			if !add(name, value.Used, reset) {
				return credentialQuota{}, errCredentialQuotaResponse
			}
		}
		switch raw.Plan {
		case "free", "plus", "pro", "team", "business", "enterprise", "edu":
			result.Subscription = &quotaSubscription{Plan: raw.Plan}
		}
	} else {
		type window struct {
			Used  *float64 `json:"utilization"`
			Reset string   `json:"resets_at"`
		}
		var raw struct {
			FiveHour *window `json:"five_hour"`
			SevenDay *window `json:"seven_day"`
			Sonnet   *window `json:"seven_day_sonnet"`
			Opus     *window `json:"seven_day_opus"`
		}
		if json.Unmarshal(data, &raw) != nil {
			return credentialQuota{}, errCredentialQuotaResponse
		}
		for index, value := range []*window{raw.FiveHour, raw.SevenDay, raw.Sonnet, raw.Opus} {
			name := []string{"five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus"}[index]
			if value != nil && !add(name, value.Used, value.Reset) {
				return credentialQuota{}, errCredentialQuotaResponse
			}
		}
	}
	if len(result.Groups[0].Buckets) == 0 {
		return credentialQuota{}, errCredentialQuotaResponse
	}
	return result, nil
}
