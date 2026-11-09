package codexappserver

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestProfileEnvironmentIsolation 删除最终环境清理会让其他平台凭据进入原生或所选 Provider。
func TestProfileEnvironmentIsolation(t *testing.T) {
	registry, ref := driverProfile(t, "high")
	var profiles []ports.ProviderProfile
	raw, err := os.ReadFile(registry.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &profiles); err != nil {
		t.Fatal(err)
	}
	profiles[0].CredentialEnv = "AO_WORKFLOW_CREDENTIAL_A"
	other := profiles[0]
	other.ID, other.CredentialEnv = "other", "AO_WORKFLOW_CREDENTIAL_B"
	raw, _ = json.Marshal(append(profiles, other))
	if err := os.WriteFile(registry.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	registry.LookupEnv = nil
	t.Setenv("AO_WORKFLOW_CREDENTIAL_A", "driver-test-secret")
	t.Setenv("AO_WORKFLOW_CREDENTIAL_B", "other-fake-secret")
	t.Setenv(ports.ProviderProfileKeyEnv, "inherited-fake-secret")
	t.Setenv("OPENAI_API_KEY", "native-fake-secret")
	choices, err := registry.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ref = choices[0].Reference
	for _, mode := range []string{"direct-profile", "direct-native", "host-profile", "host-native"} {
		t.Run(mode, func(t *testing.T) {
			d, srv := newTestDriver(t)
			d.SetProviderProfiles(registry)
			srv.reply("thread/start", `{"thread":{"id":"thread-1"},"model":"gpt-test","reasoningEffort":"high"}`)
			selected := strings.HasSuffix(mode, "profile")
			check := func(env []string, forward bool) {
				t.Helper()
				forwardCount, nativeCount := 0, 0
				for _, entry := range env {
					key, value, _ := strings.Cut(entry, "=")
					upper := strings.ToUpper(key)
					if strings.HasPrefix(upper, "AO_WORKFLOW_CREDENTIAL_") {
						t.Error("platform source credential leaked")
					}
					if upper == ports.ProviderProfileKeyEnv {
						forwardCount++
						if !forward || key != ports.ProviderProfileKeyEnv || value != "driver-test-secret" {
							t.Error("untrusted credential forwarded")
						}
					}
					if key == "OPENAI_API_KEY" && value == "native-fake-secret" {
						nativeCount++
					}
				}
				want := 0
				if forward {
					want = 1
				}
				if forwardCount != want || nativeCount != 1 {
					t.Error("selected credential or native environment lost")
				}
			}
			spawn := d.spawn
			d.spawn = func(ctx context.Context, bin, dir string, env []string) (*process, error) {
				check(env, selected)
				return spawn(ctx, bin, dir, env)
			}
			if strings.HasPrefix(mode, "host-") {
				d.persistent = true
				d.connectHost = func(ctx context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
					check(cfg.Env, false)
					prepared, err := cfg.Prepare(ctx)
					if err != nil {
						return nil, err
					}
					check(prepared.Env, selected)
					proc, err := spawn(ctx, "codex", cfg.Workdir, prepared.Env)
					if err != nil {
						return nil, err
					}
					return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout}, nil
				}
			}
			cfg := ports.ChatStartConfig{SessionID: "environment", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Model: "gpt-test", Effort: "high"}
			cfg.PrepareEnv = func(context.Context) (map[string]string, error) {
				return map[string]string{"ao_workflow_credential_b": "overlay-fake-secret", "ao_workflow_provider_key": "overlay-fake-secret"}, nil
			}
			if selected {
				cfg.Profile = ref
			}
			conv, err := d.Start(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer conv.Close()
		})
	}
}
