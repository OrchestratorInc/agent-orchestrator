package github

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// TokenSource yields a GitHub bearer token on demand. Production wires this
// to EnvTokenSource or GHTokenSource; tests inject StaticTokenSource.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// tokenInvalidator is the optional capability of dropping a cached token so
// the next call re-fetches it. The Client invokes this whenever GitHub
// responds with an auth-class failure: the next request will pick up a
// rotated token without restarting the daemon.
type tokenInvalidator interface {
	InvalidateToken()
}

// ErrNoToken is returned when no token source could yield a non-empty token.
var ErrNoToken = errors.New("github scm: no token configured")

// StaticTokenSource is a literal token, typically used in tests.
type StaticTokenSource string

// Token returns the literal token, or ErrNoToken if it is blank.
func (s StaticTokenSource) Token(context.Context) (string, error) {
	t := strings.TrimSpace(string(s))
	if t == "" {
		return "", ErrNoToken
	}
	return t, nil
}

// EnvTokenSource reads the first non-empty value from the listed env vars,
// falling back to GITHUB_TOKEN. Order matters: a project-scoped variable
// (AO_GITHUB_TOKEN) should win over the global default.
type EnvTokenSource struct {
	EnvVars []string
}

// Token returns the first non-empty env-var value found, or ErrNoToken.
func (s EnvTokenSource) Token(context.Context) (string, error) {
	for _, name := range s.EnvVars {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); v != "" {
		return v, nil
	}
	return "", ErrNoToken
}

// FallbackTokenSource tries each source in order, returning the first token. A
// source that returns ErrNoToken is skipped; other errors are remembered and
// surfaced if no later source yields a token.
type FallbackTokenSource []TokenSource

// Token returns the first non-empty token from the configured sources.
func (s FallbackTokenSource) Token(ctx context.Context) (string, error) {
	var firstErr error
	for _, src := range s {
		if src == nil {
			continue
		}
		tok, err := src.Token(ctx)
		if err == nil {
			return tok, nil
		}
		if errors.Is(err, ErrNoToken) {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return "", firstErr
	}
	return "", ErrNoToken
}

// InvalidateToken forwards cache invalidation to sources that support it.
func (s FallbackTokenSource) InvalidateToken() {
	for _, src := range s {
		if inv, ok := src.(tokenInvalidator); ok {
			inv.InvalidateToken()
		}
	}
}

const defaultGHTokenCacheTTL = 5 * time.Minute

// GHTokenSource shells out to `gh auth token` when env vars are not
// configured. It memoizes the result for TokenTTL so we don't fork-exec on
// every request, but the Client invalidates the cache on auth failures so a
// rotated token is picked up on the next call. Tests inject GH so the gh
// binary is never required.
type GHTokenSource struct {
	// GH is the shell-out hook. Production leaves this nil and falls back
	// to `exec.CommandContext("gh", "auth", "token")`; tests inject a
	// fake to avoid touching the real binary.
	GH func(ctx context.Context) (string, error)
	// Hostname selects the gh login (`gh auth token --hostname`); empty
	// means gh's default host.
	Hostname string
	// TokenTTL is how long a successful read is memoized. Zero means use
	// defaultGHTokenCacheTTL.
	TokenTTL time.Duration
	// Clock allows tests to drive expiration. Zero means time.Now.
	Clock func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// Token returns the cached token if still fresh, otherwise re-runs gh.
func (s *GHTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Before(s.expiresAt) {
		return s.token, nil
	}
	run := s.GH
	if run == nil {
		run = func(ctx context.Context) (string, error) { return ghAuthToken(ctx, s.Hostname) }
	}
	out, err := run(ctx)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(out)
	if token == "" {
		return "", ErrNoToken
	}
	s.token = token
	s.expiresAt = now.Add(s.ttl())
	return token, nil
}

// InvalidateToken drops the memoized token so the next Token call shells
// out again. The Client calls this on 401/403-auth responses.
func (s *GHTokenSource) InvalidateToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
	s.expiresAt = time.Time{}
}

func (s *GHTokenSource) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *GHTokenSource) ttl() time.Duration {
	if s.TokenTTL > 0 {
		return s.TokenTTL
	}
	return defaultGHTokenCacheTTL
}

// ghHostTokenTimeout caps a per-host gh call so a hung gh (a locked keyring)
// cannot block tenant calls forever.
const ghHostTokenTimeout = 5 * time.Second

func ghAuthToken(ctx context.Context, hostname string) (string, error) {
	if hostname == "" {
		out, err := aoprocess.CommandContext(ctx, "gh", "auth", "token").Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	ctx, cancel := context.WithTimeout(ctx, ghHostTokenTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "gh", "auth", "token", "--hostname", hostname)
	// gh applies GH_TOKEN/GITHUB_TOKEN to github.com and every *.ghe.com host.
	// They are github.com credentials, so a tenant only uses its own gh login.
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GH_TOKEN=") && !strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "", fmt.Errorf("gh auth token --hostname %s: %w", hostname, ErrNoToken)
	}
	if err != nil {
		return "", err
	}
	return string(out), nil
}
