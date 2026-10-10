package host

import (
	"context"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const accountHeader = "X-AO-Auth-ID"
const providerHeader = "X-AO-Provider"

type exactSelector struct{}

func (exactSelector) Pick(_ context.Context, provider, _ string, options executor.Options, candidates []*auth.Auth) (*auth.Auth, error) {
	id := options.Headers.Get(accountHeader)
	expectedProvider := options.Headers.Get(providerHeader)
	if id != "" && (provider == expectedProvider || provider == "mixed") {
		for _, candidate := range candidates {
			if candidate != nil && candidate.ID == id && candidate.Provider == expectedProvider && !candidate.Disabled && candidate.Status != auth.StatusDisabled {
				if options.Metadata != nil {
					options.Metadata[executor.PinnedAuthMetadataKey] = id
				}
				return candidate, nil
			}
		}
	}
	return nil, &auth.Error{Code: "account_unavailable", Message: "The session account is unavailable. Sign in or choose another account.", HTTPStatus: 503}
}
