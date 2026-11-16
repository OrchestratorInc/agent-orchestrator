package agentauth

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
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

// While an account folder signs in, the harness's own sign-in is refused with
// its own code and no operation id, so the harness's panel never adopts the
// account's sign-in as its own. A harness sign-in still names its operation.
func TestReserveLoginKeepsAccountSignInsApart(t *testing.T) {
	running := func(account bool) *Service {
		return &Service{
			operations:     map[string]*loginOperation{"op-1": {snapshot: LoginSnapshot{ID: "op-1", AgentID: "claude-code", Status: "running"}, done: make(chan struct{}), account: account}},
			terminalStarts: map[string]bool{},
		}
	}
	var apiErr *apierr.Error
	_, err := running(true).reserveLogin(context.Background(), "claude-code")
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_ACCOUNT_LOGIN_IN_PROGRESS" || apiErr.Details["operationId"] != nil {
		t.Fatalf("account sign-in in progress: err = %+v", err)
	}
	_, err = running(false).reserveLogin(context.Background(), "claude-code")
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_IN_PROGRESS" || apiErr.Details["operationId"] != "op-1" {
		t.Fatalf("harness sign-in in progress: err = %+v", err)
	}
}
