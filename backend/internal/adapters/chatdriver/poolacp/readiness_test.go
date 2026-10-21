package poolacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPoolPlatformReadiness(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows", "plan9"} {
		err := validatePlatform(goos)
		if (err == nil) != (goos == "darwin" || goos == "linux") {
			t.Fatalf("platform %s readiness = %v", goos, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := preflight(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preflight = %v", err)
	}
}

func TestPoolDiscoveryUsesExactNativeExecutableWithoutPromptOverlay(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Pool Chat requires macOS or Linux")
	}
	t.Setenv("POOLSIDE_STANDALONE_BASE_URL", "")
	t.Setenv("POOLSIDE_STANDALONE_CONTEXT_LENGTH", "")
	env := map[string]string{"POOLSIDE_STANDALONE_MODEL": "configured-model"}
	launch, err := discoveryLaunch(context.Background(), ports.AgentModelDiscoveryRequest{Binary: "/user/pool", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if launch.Command != "/user/pool" || !reflect.DeepEqual(launch.Args, []string{"acp"}) || !reflect.DeepEqual(launch.Env, env) {
		t.Fatalf("discovery launch = %#v", launch)
	}
}

func TestPoolConfigurationRejectsKnownInvalidOverridesBeforeArtifacts(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Pool Chat requires macOS or Linux")
	}
	t.Setenv("POOLSIDE_STANDALONE_BASE_URL", "")
	t.Setenv("POOLSIDE_STANDALONE_CONTEXT_LENGTH", "")
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"relative endpoint", map[string]string{"POOLSIDE_STANDALONE_BASE_URL": "secret-relative-endpoint"}},
		{"unsupported endpoint scheme", map[string]string{"POOLSIDE_STANDALONE_BASE_URL": "file:///secret"}},
		{"zero context length", map[string]string{"POOLSIDE_STANDALONE_CONTEXT_LENGTH": "0"}},
		{"noninteger context length", map[string]string{"POOLSIDE_STANDALONE_CONTEXT_LENGTH": "secret-not-a-number"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			_, _, err := configure(context.Background(), acpdriver.LaunchConfig{DataDir: dataDir, SessionID: "session", Env: tc.env})
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("configure = %v, want redacted actionable blocker", err)
			}
			if _, err := os.Stat(filepath.Join(dataDir, "prompts")); !os.IsNotExist(err) {
				t.Fatalf("invalid configuration created prompt artifacts: %v", err)
			}
			if _, err := discoveryLaunch(context.Background(), ports.AgentModelDiscoveryRequest{Binary: "/user/pool", Env: tc.env}); err == nil {
				t.Fatal("discovery accepted invalid provider configuration")
			}
		})
	}
}

func TestPoolConfigurationAllowsSavedLoginAndSessionOverrides(t *testing.T) {
	t.Setenv("POOLSIDE_STANDALONE_BASE_URL", "invalid-daemon-value")
	t.Setenv("POOLSIDE_STANDALONE_CONTEXT_LENGTH", "0")
	for _, env := range []map[string]string{
		{"POOLSIDE_STANDALONE_BASE_URL": "", "POOLSIDE_STANDALONE_CONTEXT_LENGTH": ""},
		{"POOLSIDE_STANDALONE_BASE_URL": "https://provider.example/api/v1", "POOLSIDE_STANDALONE_CONTEXT_LENGTH": "32768"},
	} {
		if err := validateConfiguration(env); err != nil {
			t.Fatalf("valid session override = %v", err)
		}
	}
	if err := validateConfiguration(nil); err == nil {
		t.Fatal("inherited malformed configuration must fail")
	}
}

func TestPoolProbeRejectsUnsupportedHostBeforeBinaryResolution(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("unsupported-host admission test")
	}
	// A nil plugin must never be reached on an unsupported host.
	_, err := New(nil, nil).Probe(context.Background())
	if !errors.Is(err, ports.ErrChatDriverUnavailable) || !strings.Contains(err.Error(), "macOS or Linux") {
		t.Fatalf("unsupported host probe = %v", err)
	}
}
