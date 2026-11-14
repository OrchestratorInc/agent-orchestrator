package gitlab

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type tokenInvalidator interface {
	InvalidateToken()
}

var ErrNoToken = errors.New("gitlab scm: no token configured")

var ErrAuthFailed = errors.New("gitlab scm: authentication failed")

type StaticTokenSource string

func (s StaticTokenSource) Token(context.Context) (string, error) {
	t := strings.TrimSpace(string(s))
	if t == "" {
		return "", ErrNoToken
	}
	return t, nil
}

type EnvTokenSource struct {
	EnvVars []string
}

func (s EnvTokenSource) Token(context.Context) (string, error) {
	for _, name := range s.EnvVars {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("GITLAB_TOKEN")); v != "" {
		return v, nil
	}
	return "", ErrNoToken
}

type FallbackTokenSource []TokenSource

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

func (s FallbackTokenSource) InvalidateToken() {
	for _, src := range s {
		if inv, ok := src.(tokenInvalidator); ok {
			inv.InvalidateToken()
		}
	}
}

const defaultGLabTokenCacheTTL = 5 * time.Minute

type GLabTokenSource struct {
	GLab     func(ctx context.Context) (string, error)
	TokenTTL time.Duration
	Clock    func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (s *GLabTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Before(s.expiresAt) {
		return s.token, nil
	}
	run := s.GLab
	if run == nil {
		run = glabAuthToken
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

func (s *GLabTokenSource) InvalidateToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
	s.expiresAt = time.Time{}
}

func (s *GLabTokenSource) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *GLabTokenSource) ttl() time.Duration {
	if s.TokenTTL > 0 {
		return s.TokenTTL
	}
	return defaultGLabTokenCacheTTL
}

func glabAuthToken(ctx context.Context) (string, error) {
	out, err := aoprocess.CommandContext(ctx, "glab", "auth", "status", "--show-token").CombinedOutput()
	if err != nil {
		return "", ErrNoToken
	}
	token := parseGLabTokenLine(string(out))
	if token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

func parseGLabTokenLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		tokenIdx := strings.Index(line, "Token")
		if tokenIdx < 0 {
			continue
		}
		colonIdx := strings.Index(line[tokenIdx:], ":")
		if colonIdx < 0 {
			continue
		}
		val := strings.TrimSpace(line[tokenIdx+colonIdx+1:])
		if val != "" {
			return val
		}
	}
	return ""
}
