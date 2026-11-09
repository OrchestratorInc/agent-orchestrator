package persistenthost

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestHostProviderEnvironment 验证真实宿主与 Provider 进程边界；删除过滤会将假秘密带进子进程。
func TestHostProviderEnvironment(t *testing.T) {
	if os.Getenv("AO_ENVIRONMENT_HELPER") == "1" {
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		leaked, forwarded, native := false, 0, false
		for _, entry := range os.Environ() {
			key, value, _ := strings.Cut(entry, "=")
			upper := strings.ToUpper(key)
			leaked = leaked || strings.HasPrefix(upper, "AO_WORKFLOW_CREDENTIAL_") || upper == rawFingerprintEnv
			if upper == ports.ProviderProfileKeyEnv {
				forwarded++
				leaked = leaked || key != ports.ProviderProfileKeyEnv || value != "selected-fake-secret"
			}
			native = native || key == "OPENAI_API_KEY" && value == "native-fake-secret"
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"leaked": leaked, "forwarded": forwarded, "native": native})
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	for _, selected := range []bool{false, true} {
		name := "native"
		if selected {
			name = "profile"
		}
		t.Run(name, func(t *testing.T) {
			cfg := Config{SessionID: name, DataDir: t.TempDir(), Workdir: t.TempDir(), Argv: []string{os.Args[0], "-test.run=^TestHostProviderEnvironment$"}}
			cfg.Env = append(os.Environ(), "AO_ENVIRONMENT_HELPER=1", "AO_WORKFLOW_CREDENTIAL_A=source-fake-secret", "ao_workflow_credential_b=other-fake-secret", ports.ProviderProfileKeyEnv+"=selected-fake-secret", "OPENAI_API_KEY=native-fake-secret")
			if selected {
				cfg.OwnershipFingerprint = "test-profile-identity"
			}
			transport, err := ConnectOrStart(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer Shutdown(context.Background(), cfg.DataDir, cfg.SessionID)
			defer transport.Stdin.Close()
			if _, err := io.WriteString(transport.Stdin, "check\n"); err != nil {
				t.Fatal(err)
			}
			var result struct {
				Leaked    bool
				Forwarded int
				Native    bool
			}
			if err := json.NewDecoder(transport.Stdout).Decode(&result); err != nil {
				t.Fatal(err)
			}
			want := 0
			if selected {
				want = 1
			}
			if result.Leaked || result.Forwarded != want || !result.Native {
				t.Fatalf("process isolation: %+v", result)
			}
		})
	}
}
