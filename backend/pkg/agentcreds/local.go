package agentcreds

import (
	"context"
	"time"
)

func (v *Validator) ValidateLocal(ctx context.Context, reportedProvider string, opts ResolveOptions) Result {
	if err := ctx.Err(); err != nil {
		return Result{State: StateUnknown, CheckedAt: time.Now(), Detail: "credential validation was canceled", Err: err}
	}
	provider, ok := ResolveProvider(reportedProvider, opts)
	if !ok {
		return Result{
			State: StateUnknown, CheckedAt: time.Now(),
			Detail: "the configured API provider is not one this build can validate",
		}
	}
	cred, found := ResolveLocal(ctx, provider, opts)
	return v.ValidateResolvedLocal(ctx, provider, cred, found, opts)
}

func ValidatesProvider(provider Provider) bool {
	return provider == ProviderFirstParty || provider == ProviderGateway
}

func (v *Validator) ValidateResolvedLocal(
	ctx context.Context,
	provider Provider,
	cred Credential,
	found bool,
	opts ResolveOptions,
) Result {
	if !ValidatesProvider(provider) {
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

	if result.State == StateInvalid && IsResolverBug(result.Detail) {
		return Result{
			State: StateUnknown, Provider: provider, Source: cred.Source,
			Fingerprint: cred.Fingerprint(), CheckedAt: result.CheckedAt, HTTPStatus: result.HTTPStatus,
			Detail: "the credential probe was malformed, so nothing can be concluded",
			Err:    result.Err,
		}
	}

	return result
}
