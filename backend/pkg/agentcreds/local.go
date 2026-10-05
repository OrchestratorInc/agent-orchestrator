package agentcreds

import (
	"context"
	"time"
)

// ValidateLocal runs the whole local story: decide the provider, resolve the
// credential that provider will use, probe it, and fall back to the provider
// CLI when the credential is chain-sourced and therefore unreadable.
//
// It is the single call the daemon needs, and it upholds the additive rule at
// every branch — anything it cannot determine comes back Unknown.
func (v *Validator) ValidateLocal(ctx context.Context, reportedProvider string, opts ResolveOptions) Result {
	if err := ctx.Err(); err != nil {
		return Result{State: StateUnknown, CheckedAt: time.Now(), Detail: "credential validation was canceled", Err: err}
	}
	provider, ok := ResolveProvider(reportedProvider, opts)
	if !ok {
		// An apiProvider this build does not recognize. Probing anything now
		// would mean guessing which host should receive the credential.
		return Result{
			State: StateUnknown, CheckedAt: time.Now(),
			Detail: "the configured API provider is not one this build can validate",
		}
	}
	cred, found := ResolveLocal(ctx, provider, opts)
	return v.ValidateResolvedLocal(ctx, provider, cred, found, opts)
}

// ValidateResolvedLocal validates a provider and credential that the caller
// already resolved from the same local environment.
func (v *Validator) ValidateResolvedLocal(
	ctx context.Context,
	provider Provider,
	cred Credential,
	found bool,
	opts ResolveOptions,
) Result {
	if provider != ProviderFirstParty && provider != ProviderGateway {
		return Result{
			State: StateUnknown, Provider: provider, CheckedAt: time.Now(),
			Detail: "credential and model validation is not supported for this Claude provider",
		}
	}

	if !found {
		return Result{
			State: StateUnknown, Provider: provider, CheckedAt: time.Now(),
			Detail: "no credential could be resolved for this provider",
		}
	}
	result := v.Validate(ctx, cred)

	// A rejection that came from AO sending a malformed request is our bug,
	// not the user's revoked credential. Downgrade it rather than telling a
	// working user to sign in again.
	if result.State == StateInvalid && IsResolverBug(result.Detail) {
		return Result{
			State: StateUnknown, Provider: provider, Source: cred.Source,
			Fingerprint: cred.Fingerprint(), CheckedAt: result.CheckedAt,
			Detail: "the credential probe was malformed, so nothing can be concluded",
			Err:    result.Err,
		}
	}

	// An expired OAuth access token is refreshable: a claude.ai subscription
	// login still holds a valid refresh token, and the agent mints a fresh
	// access token on launch. The stale access token AO happened to read is not
	// proof the user is signed out, so hand the question down to the CLI/local
	// rungs instead of reporting a working, signed-in user as "signed out". A
	// genuinely revoked token ("... is invalid") does not match and stays a
	// decisive rejection.
	if result.State == StateInvalid && cred.Kind == KindOAuthToken && IsRefreshableOAuthExpiry(result.Detail) {
		return Result{
			State: StateUnknown, Provider: provider, Source: cred.Source,
			Fingerprint: cred.Fingerprint(), CheckedAt: result.CheckedAt,
			Detail: "the OAuth access token has expired but is refreshable, so validity is unproven",
			Err:    result.Err,
		}
	}

	return result
}
