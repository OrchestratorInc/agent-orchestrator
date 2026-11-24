package dockeragent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestRejectsTerminalEvenWithTaskAndNativeIdentity(t *testing.T) {
	p := New()
	if p.Manifest().ID != "docker-agent" {
		t.Fatal(p.Manifest())
	}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "-task", NativeSessionID: "native"})
	if err == nil || !strings.Contains(err.Error(), "Chat mode only") || len(cmd) != 0 {
		t.Fatalf("cmd=%v err=%v", cmd, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, ok := any(p).(ports.AgentInterfaceHandoff); ok {
		t.Fatal("unproven interface handoff")
	}
}

// isolateProviderKeys clears provider keys and points the setup env file at an
// empty temporary config home, returning that file's path.
func isolateProviderKeys(t *testing.T) string {
	t.Helper()
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "OPENAI_API_KEY", "OPENAI_BASE_URL", "GOOGLE_API_KEY", "GEMINI_API_KEY", "GOOGLE_GEMINI_BASE_URL", ConfigEnv} {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	return filepath.Join(home, "cagent", ".env")
}

func TestDefaultConfigUsesAvailableProviders(t *testing.T) {
	isolateProviderKeys(t)
	if _, err := DefaultConfig(nil); err == nil {
		t.Fatal("default config without a provider key")
	}
	b, err := DefaultConfig(map[string]string{"OPENAI_API_KEY": "key", "GEMINI_API_KEY": "key"})
	if err != nil {
		t.Fatal(err)
	}
	config := string(b)
	if !strings.Contains(config, "root:\n    model: gpt-5.6\n") || !strings.Contains(config, "provider: google") || strings.Contains(config, "anthropic") {
		t.Fatalf("unexpected default config:\n%s", config)
	}
}

func TestDefaultConfigReadsKeysSavedBySetup(t *testing.T) {
	envFile := isolateProviderKeys(t)
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFile, []byte("# saved by docker-agent setup\nexport ANTHROPIC_API_KEY=\"key\"\nOTHER=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := DefaultConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "provider: anthropic") {
		t.Fatalf("setup env file key not used:\n%s", b)
	}
}

// fakeDoctor writes a docker-agent stand-in whose doctor prints report and
// exits with code.
func fakeDoctor(t *testing.T, report string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-in")
	}
	path := filepath.Join(t.TempDir(), "docker-agent")
	script := fmt.Sprintf("#!/bin/sh\ncat <<'JSON'\n%s\nJSON\nexit %d\n", report, code)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDoctorAuthStatus(t *testing.T) {
	const openaiFound = `{"providers":[{"provider":"anthropic","found":false},{"provider":"openai","found":true}]}`
	const onlyCopilot = `{"providers":[{"provider":"openai","found":false},{"provider":"github-copilot","found":true}]}`
	cases := []struct {
		name, report, config string
		code                 int
		want                 ports.AgentAuthStatus
		ok                   bool
	}{
		{"default config provider found", openaiFound, "", 0, ports.AgentAuthStatusConfigured, true},
		{"only providers AO cannot render", onlyCopilot, "", 0, ports.AgentAuthStatusUnauthorized, true},
		{"selected config passes", onlyCopilot, "/agent.yaml", 0, ports.AgentAuthStatusConfigured, true},
		{"selected config has issues", openaiFound, "/agent.yaml", 1, ports.AgentAuthStatusUnauthorized, true},
		{"doctor unsupported", "unknown command \"doctor\"", "", 1, ports.AgentAuthStatusUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := doctorAuthStatus(context.Background(), fakeDoctor(t, tc.report, tc.code), tc.config)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("doctorAuthStatus = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
