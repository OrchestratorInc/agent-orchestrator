package agentauth

import (
	"context"
	"testing"
)

// An account folder signs in only through the CLI's own sign-in: never a key
// AO saves, a setup token AO writes, or the guided native terminal.
func TestAccountLoginMethods(t *testing.T) {
	for _, tc := range []struct {
		agent, method string
		want          bool
	}{
		{"claude-code", "browser", true},
		{"claude-code", "console", true},
		{"claude-code", "sso", true},
		{"claude-code", claudeSetupTokenMethod, false},
		{"claude-code", "env_ANTHROPIC_API_KEY", false},
		{"claude-code", "native", false},
		{"codex", "browser", true},
		{"codex", "device_code", true},
		{"codex", "api_key", true},
		{"codex", "access_token", true},
		{"opencode", "anthropic_key", false},
		{"claude-code", "unknown", false},
	} {
		if got := AccountLoginMethod(tc.agent, tc.method); got != tc.want {
			t.Errorf("AccountLoginMethod(%q, %q) = %v, want %v", tc.agent, tc.method, got, tc.want)
		}
	}
}

// An account login with a method it cannot run, or without its folder, is
// refused before anything is started or saved.
func TestStartAccountLoginRefusesOtherMethods(t *testing.T) {
	s := &Service{}
	env := map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()}
	for _, method := range []string{"env_ANTHROPIC_API_KEY", claudeSetupTokenMethod, "native"} {
		if _, err := s.StartAccountLogin(context.Background(), "claude-code", method, "secret", env); err == nil {
			t.Fatalf("StartAccountLogin(%q) succeeded", method)
		}
	}
	if _, err := s.StartAccountLogin(context.Background(), "claude-code", "browser", "", nil); err == nil {
		t.Fatal("StartAccountLogin without an account folder succeeded")
	}
}
