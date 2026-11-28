package agentcreds

import (
	"context"
	"time"
)

// HasUsableSubscriptionLogin reports whether Claude Code has a claude.ai
// subscription login of its own stored for opts (the keychain on macOS, else
// .credentials.json), one it can still use or renew. Credential environment
// variables are ignored: only `claude auth login` writes this login, never a
// `claude setup-token` token or an API key.
//
// It decides whether a setup token AO saved should sign a session in. A setup
// token is limited to inference, so Claude Code cannot load the user's claude.ai
// connectors (Gmail, Calendar, Drive, ...) with it (#9101). A usable full login
// therefore wins over the token.
func HasUsableSubscriptionLogin(ctx context.Context, opts ResolveOptions, now time.Time) bool {
	if opts.DisableStoredCredentials {
		return false
	}
	stored, _, ok := loadOAuth(ctx, opts)
	if !ok || stored.kind != KindOAuthToken {
		return false
	}
	if stored.expiresAt.IsZero() || now.Before(stored.expiresAt) {
		return true
	}
	credential := Credential{
		Kind: stored.kind, ExpiresAt: stored.expiresAt,
		Renewable: stored.renewable, RefreshExpiresAt: stored.refreshExpiresAt,
	}
	return credential.ExpiredButRenewable(now)
}
