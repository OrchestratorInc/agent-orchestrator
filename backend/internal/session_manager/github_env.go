package sessionmanager

import (
	"context"
	"os"
	"strings"
)

// gitHubCredentialHelper answers git's credential "get" request for
// github.com with the session's GH_TOKEN, so HTTPS pushes work without the
// user configuring git credentials. Helpers the user already configured run
// first; this one only fills in when they have nothing.
const gitHubCredentialHelper = `!f() { test "$1" = get && echo username=x-access-token && echo "password=$GH_TOKEN"; }; f`

// applyGitHubEnv gives the session the token from AO's own GitHub sign-in:
// GH_TOKEN for the gh CLI and a git credential helper for HTTPS pushes. A
// token the user already provides through the environment or project env
// wins, and nothing is added when the user has not signed in through AO.
func (m *Manager) applyGitHubEnv(env, projectEnv map[string]string, caseInsensitive bool) {
	if m.githubToken == nil {
		return
	}
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if os.Getenv(key) != "" || envHasKey(projectEnv, key, caseInsensitive) {
			return
		}
	}
	token, err := m.githubToken(context.Background())
	if err != nil || strings.TrimSpace(token) == "" {
		return
	}
	setProtectedEnv(env, "GH_TOKEN", token, caseInsensitive)
	// GIT_CONFIG_COUNT adds config without touching the user's files. Leave a
	// count the user set alone rather than renumbering their entries.
	if os.Getenv("GIT_CONFIG_COUNT") != "" || envHasKey(projectEnv, "GIT_CONFIG_COUNT", caseInsensitive) {
		return
	}
	setProtectedEnv(env, "GIT_CONFIG_COUNT", "1", caseInsensitive)
	setProtectedEnv(env, "GIT_CONFIG_KEY_0", "credential.https://github.com.helper", caseInsensitive)
	setProtectedEnv(env, "GIT_CONFIG_VALUE_0", gitHubCredentialHelper, caseInsensitive)
}

func envHasKey(env map[string]string, key string, caseInsensitive bool) bool {
	for existing, value := range env {
		if value == "" {
			continue
		}
		if existing == key || (caseInsensitive && strings.EqualFold(existing, key)) {
			return true
		}
	}
	return false
}
