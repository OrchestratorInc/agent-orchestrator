package github

import (
	"context"
	"errors"
	"os"
	"strings"
)

type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

var ErrNoToken = errors.New("github tracker: no token configured")

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
	if v := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); v != "" {
		return v, nil
	}
	return "", ErrNoToken
}
