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

const keychainTimeout = 3 * time.Second

const (
	keychainServiceCredentials = "Claude Code-credentials"
	keychainServiceManagedKey  = "Claude Code"
	keychainFallbackAccount    = "claude-code-user"
	keychainExitItemNotFound   = 44
)

var keychainAccountName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func keychainItemMissing(err error) bool {
	var exit interface{ ExitCode() int }
	return errors.As(err, &exit) && exit.ExitCode() == keychainExitItemNotFound
}

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
		hash := sha256.Sum256([]byte(configDir))
		suffix = fmt.Sprintf("-%x", hash[:4])
	}
	account := keychainAccount(opts)
	lookup := func(service string) ([]byte, error) {
		out, err := runner(probeCtx, "security", "find-generic-password", "-s", service, "-a", account, "-w")
		if probeCtx.Err() != nil || !keychainItemMissing(err) {
			return out, err
		}
		return runner(probeCtx, "security", "find-generic-password", "-s", service, "-w")
	}

	out, err := lookup(keychainServiceCredentials + suffix)
	if probeCtx.Err() != nil {
		return storedOAuth{}, false
	}
	if err == nil {
		if stored, ok := oauthTokenFromCredentialsJSON(out); ok {
			return stored, true
		}
		if raw := strings.TrimSpace(string(out)); raw != "" && !strings.HasPrefix(raw, "{") {
			return storedOAuth{token: raw, kind: KindOAuthToken}, true
		}
	}

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

func kindForToken(token string) Kind {
	if strings.HasPrefix(strings.TrimSpace(token), "sk-ant-oat") {
		return KindOAuthToken
	}
	return KindAPIKey
}
