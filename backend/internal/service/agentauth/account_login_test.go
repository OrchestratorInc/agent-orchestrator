package agentauth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
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

// An account folder's sign-in leaves the computer's own login alone: an earlier
// setup token and an API key saved from Claude Code's panel both stay.
func TestAccountSignInKeepsTheComputersOwnLogin(t *testing.T) {
	svc := loginTestService(t, "succeed")
	svc.executables = executableFinderFunc(func(name string) (string, error) { return "/test/bin/" + name, nil })
	svc.dataDir = t.TempDir()
	if err := claudecode.SaveLocalOAuthToken(svc.dataDir, []byte("sk-ant-oat01-earlier-setup-token")); err != nil {
		t.Fatal(err)
	}
	claimAnthropicKeyForClaude(t, svc.dataDir)
	started, err := svc.StartAccountLogin(t.Context(), "claude-code", "browser", "", map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot, pollErr := svc.Login(t.Context(), "claude-code", started.ID)
		if pollErr != nil {
			t.Fatal(pollErr)
		}
		if snapshot.Status != "running" {
			if snapshot.Status != "succeeded" {
				t.Fatalf("snapshot=%+v", snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("account sign-in did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(claudecode.LocalOAuthTokenPath(svc.dataDir)); err != nil {
		t.Fatalf("an account sign-in removed the computer's setup token: %v", err)
	}
	if inject, withhold, err := providerkeys.ForSession(svc.dataDir, "claude-code"); err != nil || inject["ANTHROPIC_API_KEY"] != "sk-ant-api03-earlier" || len(withhold) != 0 {
		t.Fatalf("an account sign-in released Claude Code's key: inject=%v withhold=%v err=%v", inject, withhold, err)
	}
}
