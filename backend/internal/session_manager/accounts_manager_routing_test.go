package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeAccountsManagerRouter struct {
	route   *ports.AccountsManagerLaunchRoute
	err     error
	enabled bool
}

func (f fakeAccountsManagerRouter) PrepareAgentLaunchRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider, string) (*ports.AccountsManagerLaunchRoute, error) {
	return f.route, f.err
}

func (f fakeAccountsManagerRouter) AgentRoutingEnabled(context.Context, domain.AccountsManagerProvider) (bool, error) {
	return f.enabled, f.err
}

func (f fakeAccountsManagerRouter) HasAgentSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (bool, error) {
	return f.route != nil, f.err
}

func TestManagedSessionCannotTransitionToNativeChat(t *testing.T) {
	m, st, _, _ := newManager()
	m.chat = &transitionChat{}
	m.accountsManager = fakeAccountsManagerRouter{route: &ports.AccountsManagerLaunchRoute{}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessCodex
	err := m.preflightInterfaceTarget(context.Background(), rec, domain.SessionInterfaceTransition{TargetMode: domain.SessionModeChat})
	if !errors.Is(err, ports.ErrChatUnsupported) || !strings.Contains(err.Error(), "Accounts Manager") {
		t.Fatalf("managed transition = %v", err)
	}
}

func TestPrepareAccountsManagerRouteInjectsOnlyChildScopedCodexConfiguration(t *testing.T) {
	m := New(Deps{AccountsManager: fakeAccountsManagerRouter{route: &ports.AccountsManagerLaunchRoute{BaseURL: "http://127.0.0.1:43127", Token: "opaque-token"}}})
	env := map[string]string{"OPENAI_API_KEY": "native-key"}
	route, err := m.prepareAccountsManagerRoute(context.Background(), "session-1", domain.HarnessCodex, "gpt-5", env)
	if err != nil {
		t.Fatal(err)
	}
	if route == nil || route.BaseURL != "http://127.0.0.1:43127" || route.TokenEnv != accountsManagerCodexTokenEnv {
		t.Fatalf("route = %#v", route)
	}
	if env[accountsManagerCodexTokenEnv] != "opaque-token" || env["OPENAI_API_KEY"] != "native-key" {
		t.Fatalf("env = %#v", env)
	}
}

func TestPrepareAccountsManagerRouteReplacesConflictingClaudeCredentials(t *testing.T) {
	m := New(Deps{AccountsManager: fakeAccountsManagerRouter{route: &ports.AccountsManagerLaunchRoute{BaseURL: "http://127.0.0.1:43127/", Token: "opaque-token"}}})
	env := map[string]string{"anthropic_api_key": "native-key", "Claude_Code_OAuth_Token": "native-oauth"}
	_, err := m.prepareAccountsManagerRoute(context.Background(), "session-1", domain.HarnessClaudeCode, "sonnet", env)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := env["anthropic_api_key"]; ok {
		t.Fatal("case-insensitive ANTHROPIC_API_KEY was not removed")
	}
	if _, ok := env["Claude_Code_OAuth_Token"]; ok {
		t.Fatal("case-insensitive CLAUDE_CODE_OAUTH_TOKEN was not removed")
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:43127" || env["ANTHROPIC_AUTH_TOKEN"] != "opaque-token" {
		t.Fatalf("env = %#v", env)
	}
}

func TestPrepareAccountsManagerRouteLeavesNativeEnvironmentUntouchedWhenOff(t *testing.T) {
	m := New(Deps{AccountsManager: fakeAccountsManagerRouter{}})
	env := map[string]string{"ANTHROPIC_API_KEY": "native-key"}
	route, err := m.prepareAccountsManagerRoute(context.Background(), "session-1", domain.HarnessClaudeCode, "sonnet", env)
	if err != nil || route != nil || env["ANTHROPIC_API_KEY"] != "native-key" {
		t.Fatalf("route=%#v env=%#v err=%v", route, env, err)
	}
}

func TestAccountsManagerRoutePrecedesLaunchAuthValidation(t *testing.T) {
	for _, operation := range []string{"spawn", "restore", "switch", "interface"} {
		t.Run(operation, func(t *testing.T) {
			m, st, rt, _ := newManager()
			m.accountsManager = fakeAccountsManagerRouter{
				enabled: operation != "switch",
				route:   &ports.AccountsManagerLaunchRoute{BaseURL: "http://127.0.0.1:43127", Token: "route-token"},
			}
			agent := &launchAuthAgent{recordingAgent: &recordingAgent{}, status: ports.AgentAuthStatusUnauthorized}
			m.agents = singleAgent{agent: agent}
			seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "native-1"})
			rec := st.sessions["mer-1"]
			rec.Harness = domain.HarnessCodex
			st.sessions[rec.ID] = rec
			wantErr := ports.ErrAgentAuthRequired
			var err error
			switch operation {
			case "spawn":
				delete(st.sessions, rec.ID)
				_, _, _, err = m.Spawn(context.Background(), ports.SpawnConfig{
					ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
					RequestedMode: domain.SessionModeTUI,
				})
			case "restore":
				_, err = m.RestoreWithMode(context.Background(), rec.ID)
			case "switch":
				readiness := &switchReadinessProvider{}
				readiness.snapshot.Authentication.State = domain.AgentAuthenticationUnauthorized
				m.agentReadiness = readiness
				_, err = m.prepareTargetActivation(context.Background(), nil, rec, st.projects["mer"], agent,
					ports.ContinuationCapabilities{}, domain.AgentSwitch{TargetHarness: domain.HarnessCodex}, "")
				wantErr = ErrTargetAgentUnauthorized
			case "interface":
				err = m.preflightInterfaceTarget(context.Background(), rec, domain.SessionInterfaceTransition{
					TargetMode: domain.SessionModeTUI, NativeConversationID: "native-1",
				})
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
			if agent.env[accountsManagerCodexTokenEnv] != "route-token" || agent.workingDir != "/ws/mer-1" {
				t.Fatal("launch validation did not receive the routed workspace environment")
			}
			if rt.created != 0 {
				t.Fatal("runtime started after launch authentication was rejected")
			}
		})
	}
}
