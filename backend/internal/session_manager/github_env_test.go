package sessionmanager

import (
	"context"
	"errors"
	"testing"
)

func TestApplyGitHubEnv(t *testing.T) {
	signedIn := func(context.Context) (string, error) { return "gho_saved", nil }
	signedOut := func(context.Context) (string, error) { return "", errors.New("not signed in") }
	for name, tc := range map[string]struct {
		token      func(context.Context) (string, error)
		processEnv map[string]string
		projectEnv map[string]string
		wantToken  string
		wantHelper bool
	}{
		"signed in":                     {token: signedIn, wantToken: "gho_saved", wantHelper: true},
		"not signed in":                 {token: signedOut},
		"daemon GITHUB_TOKEN wins":      {token: signedIn, processEnv: map[string]string{"GITHUB_TOKEN": "env"}},
		"project GH_TOKEN wins":         {token: signedIn, projectEnv: map[string]string{"GH_TOKEN": "project"}},
		"user git config count is kept": {token: signedIn, projectEnv: map[string]string{"GIT_CONFIG_COUNT": "2"}, wantToken: "gho_saved"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("GH_TOKEN", "")
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("GIT_CONFIG_COUNT", "")
			for key, value := range tc.processEnv {
				t.Setenv(key, value)
			}
			m := New(Deps{GitHubToken: tc.token})
			env := map[string]string{}
			for key, value := range tc.projectEnv {
				env[key] = value
			}
			m.applyGitHubEnv(env, tc.projectEnv, false)

			if tc.wantToken == "" {
				if token, ok := env["GH_TOKEN"]; ok && tc.projectEnv["GH_TOKEN"] == "" {
					t.Fatalf("GH_TOKEN = %q, want unset", token)
				}
			} else if env["GH_TOKEN"] != tc.wantToken {
				t.Fatalf("GH_TOKEN = %q, want %q", env["GH_TOKEN"], tc.wantToken)
			}
			gotHelper := env["GIT_CONFIG_KEY_0"] == "credential.https://github.com.helper" && env["GIT_CONFIG_VALUE_0"] == gitHubCredentialHelper
			if gotHelper != tc.wantHelper {
				t.Fatalf("credential helper set = %v, want %v (env %v)", gotHelper, tc.wantHelper, env)
			}
		})
	}
}
