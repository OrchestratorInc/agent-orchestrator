//go:build e2e && linux

package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedRouteInstalledConfiguration(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("installed Codex required")
	}
	wrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("network-isolated child requires bubblewrap")
	}
	scratch := t.TempDir()
	home := filepath.Join(scratch, "codex")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	auth := []byte(`{"OPENAI_API_KEY":"synthetic-native-key","auth_mode":"apikey"}`)
	config := []byte("cli_auth_credentials_store = 'file'\nmodel_provider = 'openai'\ncheck_for_update_on_startup = false\n[analytics]\nenabled = false\n")
	for path, data := range map[string][]byte{"auth.json": auth, "config.toml": config} {
		if err := os.WriteFile(filepath.Join(home, path), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	isolatedCommand := func(ctx context.Context, args ...string) *exec.Cmd {
		wrapped := []string{"--die-with-parent", "--unshare-net", "--unshare-pid", "--ro-bind", "/", "/",
			"--tmpfs", "/home", "--proc", "/proc", "--dev", "/dev", "--bind", scratch, scratch, "--chdir", scratch, binary}
		cmd := exec.CommandContext(ctx, wrap, append(wrapped, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + scratch, "CODEX_HOME=" + home,
			"XDG_CONFIG_HOME=" + scratch, "XDG_CACHE_HOME=" + scratch, "TMPDIR=" + scratch,
			"TERM=dumb", "AO_ACCOUNTS_MANAGER_SESSION_TOKEN=synthetic-route-token"}
		cmd.WaitDelay = time.Second
		return cmd
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	version, err := isolatedCommand(ctx, "--version").Output()
	if err != nil {
		t.Fatalf("isolated binary version: %v", err)
	}
	t.Log(strings.TrimSpace(string(version)))
	for _, mode := range []string{"native", "account-a", "account-b", "native-after-managed"} {
		t.Run(mode, func(t *testing.T) {
			var route *ports.AgentProviderRoute
			if mode == "account-a" || mode == "account-b" {
				port := 43127
				if mode == "account-b" {
					port++
				}
				route = &ports.AgentProviderRoute{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), TokenEnv: "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"}
			}
			command, err := (&Plugin{resolvedBinary: binary}).GetLaunchCommand(ctx, ports.LaunchConfig{Route: route})
			if err != nil {
				t.Fatal(err)
			}
			var overrides []string
			for i := 0; i+1 < len(command); i++ {
				if command[i] == "-c" {
					overrides = append(overrides, command[i], command[i+1])
					i++
				}
			}
			statusArgs := append(append([]string(nil), overrides...), "login", "status")
			status, statusErr := isolatedCommand(ctx, statusArgs...).CombinedOutput()
			if route == nil {
				if statusErr != nil || !bytes.Contains(status, []byte("Logged in")) {
					t.Fatal("synthetic native file authentication control was not loaded")
				}
			} else if statusErr == nil || !bytes.Contains(status, []byte("Not logged in")) {
				t.Fatal("managed command loaded ambient native authentication or did not expose the expected status")
			}
			appArgs := append(append([]string(nil), overrides...), "app-server")
			proc := isolatedCommand(ctx, appArgs...)
			stdin, err := proc.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := proc.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := proc.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = stdin.Close()
				if err := proc.Wait(); err != nil {
					t.Errorf("isolated protocol child did not exit normally: %v", err)
				}
			}()
			encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
			request := func(id int, method string, params any) map[string]json.RawMessage {
				t.Helper()
				if err := encoder.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
					t.Fatal(err)
				}
				for {
					var response struct {
						ID     *int                       `json:"id"`
						Result map[string]json.RawMessage `json:"result"`
						Error  json.RawMessage            `json:"error"`
					}
					if err := decoder.Decode(&response); err != nil {
						t.Fatalf("%s response: %v", method, err)
					}
					if response.ID == nil || *response.ID != id {
						continue
					}
					if len(response.Error) != 0 {
						t.Fatalf("%s returned a protocol error", method)
					}
					return response.Result
				}
			}
			request(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "ao-route-check", "version": "1.0"}})
			if err := encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
				t.Fatal(err)
			}
			result := request(2, "config/read", map[string]any{"includeLayers": true})
			var effective map[string]json.RawMessage
			if err := json.Unmarshal(result["config"], &effective); err != nil {
				t.Fatal(err)
			}
			wantProvider, wantStore := `"openai"`, `"file"`
			if route != nil {
				wantProvider, wantStore = `"ao_accounts_manager"`, `"ephemeral"`
			}
			if string(effective["model_provider"]) != wantProvider || string(effective["cli_auth_credentials_store"]) != wantStore {
				t.Fatalf("effective provider/store did not match the requested mode: %s/%s", effective["model_provider"], effective["cli_auth_credentials_store"])
			}
			if route != nil {
				var providers map[string]struct {
					BaseURL string `json:"base_url"`
					EnvKey  string `json:"env_key"`
				}
				if err := json.Unmarshal(effective["model_providers"], &providers); err != nil {
					t.Fatal("effective managed provider configuration missing")
				}
				got := providers["ao_accounts_manager"]
				if got.BaseURL != route.BaseURL+"/v1" || got.EnvKey != route.TokenEnv {
					t.Error("effective managed provider did not retain the exact session route")
				}
			}
			for path, original := range map[string][]byte{"auth.json": auth, "config.toml": config} {
				current, err := os.ReadFile(filepath.Join(home, path))
				if err != nil || !bytes.Equal(current, original) {
					t.Errorf("%s changed during managed configuration checks", path)
				}
			}
		})
	}
}
