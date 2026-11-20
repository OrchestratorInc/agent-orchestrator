package agentcreds

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os/user"
	"regexp"
	"strings"
	"time"
)

// keychainTimeout hard-bounds the keychain read.
//
// A measured read takes 75–110ms, prompts nothing, and works from a detached
// process with no TTY. But that is a property of how Claude Code stores this
// particular item today, not a platform guarantee — a differently-stored item
// can put a GUI unlock dialog in front of the read and block until someone
// clicks it. The design must not depend on the fast path, so the call is
// capped and a timeout resolves to "no credential", never to a hang.
const keychainTimeout = 3 * time.Second

// Keychain service names Claude Code has used to store its credential.
const (
	// keychainServiceCredentials is the older service. It stores a JSON
	// document with an OAuth access token, or occasionally a bare token.
	keychainServiceCredentials = "Claude Code-credentials"
	// keychainServiceManagedKey is the service Claude Code v2.1.268+ uses
	// for the /login managed key. The value is a raw sk-ant-api* key.
	keychainServiceManagedKey = "Claude Code"
	// keychainFallbackAccount is the account Claude Code files its credential
	// under when the user's name is unavailable or not usable as one.
	keychainFallbackAccount = "claude-code-user"
	// keychainExitItemNotFound is how `security` reports errSecItemNotFound.
	keychainExitItemNotFound = 44
)

var keychainAccountName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// keychainItemMissing reports that `security` ran and found no such item, as
// opposed to finding one it could not read (a denied prompt, a locked keychain).
func keychainItemMissing(err error) bool {
	var exit interface{ ExitCode() int }
	return errors.As(err, &exit) && exit.ExitCode() == keychainExitItemNotFound
}

// keychainAccount names the account Claude Code reads and writes its credential
// under, by the same rule: $USER, then the OS user.
func keychainAccount(opts ResolveOptions) string {
	name := opts.env("USER")
	if name == "" {
		current, err := user.Current()
		if err != nil {
			return keychainFallbackAccount
		}
		name = current.Username
	}
	if !keychainAccountName.MatchString(name) {
		return keychainFallbackAccount
	}
	return name
}

// readKeychain reads the Claude Code subscription token from the macOS
// keychain via the `security` helper.
//
// Every failure mode resolves the same way — no credential, caller reports
// Unknown — so the distinctions below are for diagnosis, not control flow.
// That is deliberate: a locked keychain must never be reported as a missing
// or invalid credential, because the user is very likely signed in perfectly
// well and simply has the keychain locked.
func readKeychain(ctx context.Context, opts ResolveOptions) (storedOAuth, bool) {
	runner := opts.Runner
	if runner == nil {
		runner = execCommand
	}
	probeCtx, cancel := context.WithTimeout(ctx, keychainTimeout)
	defer cancel()
	configDir := opts.env("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	if configDir == "" {
		configDir = strings.TrimSpace(opts.ConfigDir)
		if configDir == "" {
			configDir = opts.env("CLAUDE_CONFIG_DIR")
		}
	}
	suffix := ""
	if configDir != "" {
		// Claude keys account logins on the exact path text, without cleaning it.
		hash := sha256.Sum256([]byte(configDir))
		suffix = fmt.Sprintf("-%x", hash[:4])
	}
	account := keychainAccount(opts)
	// One service can hold items under several accounts, and a lookup without an
	// account returns the least recently written, so an item left by an old login
	// shadows the one Claude Code keeps refreshing. Ask for its own account first. The
	// lookup without one remains for an item filed under another account name, so
	// it runs only when that account has no item: repeating a read that failed
	// could prompt again or return the stale item.
	lookup := func(service string) ([]byte, error) {
		out, err := runner(probeCtx, "security", "find-generic-password", "-s", service, "-a", account, "-w")
		if probeCtx.Err() != nil || !keychainItemMissing(err) {
			return out, err
		}
		return runner(probeCtx, "security", "find-generic-password", "-s", service, "-w")
	}

	// Try the older "Claude Code-credentials" service first. It stores a JSON
	// document with an OAuth access token, or occasionally a bare token.
	out, err := lookup(keychainServiceCredentials + suffix)
	if probeCtx.Err() != nil {
		return storedOAuth{}, false
	}
	if err == nil {
		if stored, ok := oauthTokenFromCredentialsJSON(out); ok {
			return stored, true
		}
		// Older entries store the bare token rather than a JSON document.
		if raw := strings.TrimSpace(string(out)); raw != "" && !strings.HasPrefix(raw, "{") {
			return storedOAuth{token: raw, kind: KindOAuthToken}, true
		}
	}

	// Fall through to "Claude Code", the service Claude Code v2.1.268+ uses
	// for the /login managed key. The value is a raw sk-ant-api* key, though
	// a setup token (sk-ant-oat*) could also land here, so the Kind is
	// chosen by prefix rather than assumed.
	apiKeyOut, apiKeyErr := lookup(keychainServiceManagedKey + suffix)
	if probeCtx.Err() != nil {
		return storedOAuth{}, false
	}
	if apiKeyErr != nil {
		return storedOAuth{}, false
	}
	if raw := strings.TrimSpace(string(apiKeyOut)); raw != "" && !strings.HasPrefix(raw, "{") {
		return storedOAuth{token: raw, kind: kindForToken(raw)}, true
	}
	if stored, ok := oauthTokenFromCredentialsJSON(apiKeyOut); ok {
		return stored, true
	}
	return storedOAuth{}, false
}

// kindForToken selects the auth header from the token prefix. Claude Code
// console keys are sk-ant-api* (x-api-key); setup tokens and subscription
// logins are sk-ant-oat* (Bearer). Anything unrecognised defaults to API key
// so the probe still runs rather than silently skipping a credential.
func kindForToken(token string) Kind {
	if strings.HasPrefix(strings.TrimSpace(token), "sk-ant-oat") {
		return KindOAuthToken
	}
	return KindAPIKey
}
