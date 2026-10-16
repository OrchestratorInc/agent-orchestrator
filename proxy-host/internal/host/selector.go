package host

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const accountHeader, providerHeader = "X-AO-Auth-ID", "X-AO-Provider"

// exactSelector picks the account the boundary named for the request, and never another.
type exactSelector struct{ m *auth.Manager }

func (s exactSelector) Pick(_ context.Context, provider, model string, options executor.Options, candidates []*auth.Auth) (*auth.Auth, error) {
	id, want := options.Headers.Get(accountHeader), options.Headers.Get(providerHeader)
	for _, candidate := range candidates {
		if id != "" && (provider == want || provider == "mixed") && candidate != nil && candidate.ID == id && candidate.Provider == want && !candidate.Disabled && candidate.Status != auth.StatusDisabled {
			if options.Metadata != nil {
				options.Metadata[executor.PinnedAuthMetadataKey] = id
			}
			return candidate, nil
		}
	}
	// The SDK leaves out an account it rests for the model; the session hears the refusal that started the rest, in the provider's words alone.
	if s.m != nil {
		if a, ok := s.m.GetByID(id); ok && a.Provider == want && !a.Disabled && a.Status != auth.StatusDisabled {
			if e := rested(a, model, time.Now()); e != nil && e.HTTPStatus != 0 {
				return nil, &auth.Error{Message: e.Message, HTTPStatus: e.HTTPStatus}
			}
		}
	}
	return nil, &auth.Error{Code: "account_unavailable", Message: "The session account is unavailable. Sign in or choose another account.", HTTPStatus: 503}
}

// rested is the refusal behind a rest of the account that still holds for the model: one across the account, or the model's own.
func rested(a *auth.Auth, model string, now time.Time) *auth.Error {
	if a.Quota.Exceeded && a.Quota.Reason == "credential_quota" && a.Quota.NextRecoverAt.After(now) {
		return a.LastError
	}
	if state := a.ModelStates[model]; state != nil && state.Unavailable && state.NextRetryAfter.After(now) {
		return state.LastError
	}
	return nil
}

// requestRefusal keeps a request the provider rejects, such as one for a model the account's plan refuses, off the account:
// the SDK would rest the model there for hours, and answer each later request at once with a retryable 503. A refused sign-in still rests it.
func requestRefusal(_ context.Context, r auth.Result) auth.Result {
	if e := r.Error; e != nil && !r.CredentialScope && slices.Contains([]int{400, 404, 422}, e.HTTPStatus) && !strings.Contains(strings.ToLower(e.Code+" "+e.Message), "invalid_grant") {
		scoped := *e
		scoped.Code = auth.ErrorCodeRequestScoped
		r.Error = &scoped
	}
	return r
}
