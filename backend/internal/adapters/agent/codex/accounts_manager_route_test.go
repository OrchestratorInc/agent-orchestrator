package codex

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedRouteConfiguration(t *testing.T) {
	builders := map[string]func(*ports.AgentProviderRoute) ([]string, error){
		"launch": func(route *ports.AgentProviderRoute) ([]string, error) {
			return (&Plugin{resolvedBinary: "codex"}).GetLaunchCommand(context.Background(), ports.LaunchConfig{Route: route})
		},
		"restore": func(route *ports.AgentProviderRoute) ([]string, error) {
			cmd, ok, err := (&Plugin{resolvedBinary: "codex"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{
				Route: route, Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "thread-existing"}},
			})
			if err == nil && !ok {
				t.Fatal("restore fixture did not produce a command")
			}
			return cmd, err
		},
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			native, err := build(nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []struct {
				name, baseURL, tokenEnv string
			}{
				{"empty", "", ""},
				{"missing-token", "http://127.0.0.1:43127", ""},
				{"missing-url", "", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"remote", "https://example.com", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"userinfo", "http://synthetic-private@127.0.0.1:43127", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"query", "http://127.0.0.1:43127/?token=synthetic-private", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"fragment", "http://127.0.0.1:43127/#synthetic-private", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"path", "http://127.0.0.1:43127/private", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"missing-port", "http://127.0.0.1", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"zero-port", "http://127.0.0.1:0", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"overflow-port", "http://127.0.0.1:65536", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"hostname", "http://localhost:43127", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"alternate-loopback", "http://127.0.0.2:43127", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"https", "https://127.0.0.1:43127", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"invalid-url", "http://127.0.0.1:bad", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"escaped-path", "http://127.0.0.1:43127/%2F", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"empty-query", "http://127.0.0.1:43127/?", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"empty-fragment", "http://127.0.0.1:43127/#", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"},
				{"invalid-env", "http://127.0.0.1:43127", "synthetic-private=oops"},
				{"native-env", "http://127.0.0.1:43127", "OPENAI_API_KEY"},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					cmd, err := build(&ports.AgentProviderRoute{BaseURL: invalid.baseURL, TokenEnv: invalid.tokenEnv})
					if err == nil || len(cmd) != 0 {
						t.Error("invalid managed route returned executable argv or no error")
					}
					if err != nil && strings.Contains(err.Error(), "synthetic-private") {
						t.Error("invalid route error exposed input")
					}
				})
			}
			t.Run("managed", func(t *testing.T) {
				cmd, err := build(&ports.AgentProviderRoute{BaseURL: "http://127.0.0.1:43127/", TokenEnv: "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"})
				if err != nil {
					t.Fatal(err)
				}
				for _, required := range []string{
					`cli_auth_credentials_store='ephemeral'`,
					`model_provider='ao_accounts_manager'`,
					`model_providers.ao_accounts_manager.base_url='http://127.0.0.1:43127/v1'`,
					`model_providers.ao_accounts_manager.env_key='AO_ACCOUNTS_MANAGER_SESSION_TOKEN'`,
					`model_providers.ao_accounts_manager.requires_openai_auth=false`,
				} {
					if !hasExactConfig(cmd, required) {
						t.Errorf("missing managed override %s", required)
					}
				}
			})
			t.Run("native-preserved", func(t *testing.T) {
				again, err := build(nil)
				if err != nil || !reflect.DeepEqual(native, again) {
					t.Error("managed command construction changed native launch state")
				}
				for _, arg := range native {
					if strings.Contains(arg, "cli_auth_credentials_store") || strings.Contains(arg, "ao_accounts_manager") {
						t.Error("native command received a managed override")
					}
				}
			})
		})
	}
	t.Run("restore-without-history", func(t *testing.T) {
		cmd, ok, err := (&Plugin{resolvedBinary: "codex"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{Route: &ports.AgentProviderRoute{}})
		if err == nil || ok || len(cmd) != 0 {
			t.Error("invalid route became a fresh-launch fallback when history was absent")
		}
	})
}

func hasExactConfig(args []string, value string) bool {
	for i := 1; i < len(args); i++ {
		if args[i-1] == "-c" && args[i] == value {
			return true
		}
	}
	return false
}
