package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGeminiNativeDiscoveryPreservesConfigurationAndDisablesStartupWork(t *testing.T) {
	data := t.TempDir()
	system := filepath.Join(data, "original.json")
	original := []byte(`{"model":{"name":"configured-native-model"},"security":{"auth":{"selectedType":"oauth-personal"}},"policy":{"exact":9007199254740993},"experimental":{"autoMemory":true,"other":true},"hooksConfig":{"enabled":true,"disabled":["old"]},"admin":{"mcp":{"enabled":true,"config":{"server":"preserved"}},"extensions":{"enabled":true,"other":"preserved"},"other":"preserved"}}`)
	writeConfig(t, system, string(original))
	request := ports.AgentModelDiscoveryRequest{Binary: "gemini", WorkingDir: data, Env: map[string]string{
		"AO_DATA_DIR": data, "GEMINI_CLI_SYSTEM_SETTINGS_PATH": system, "GEMINI_API_KEY": "test-key",
	}}
	var clone string
	want := []ports.ChatConfigOption{{ID: "model"}}
	got, err := discoverGeminiOptions(context.Background(), request, nil, func(_ context.Context, launch acpdriver.Launch, cwd string, _ *slog.Logger) ([]ports.ChatConfigOption, error) {
		if cwd != data || launch.Command != "gemini" || !reflect.DeepEqual(launch.Args, []string{"--acp", "--extensions", "none"}) {
			t.Fatalf("launch = %#v, cwd = %s", launch, cwd)
		}
		if launch.Env["GEMINI_API_KEY"] != "test-key" || launch.Env["GEMINI_CLI_SYSTEM_DEFAULTS_PATH"] != filepath.Join(data, "system-defaults.json") {
			t.Fatal("credentials or original system-defaults path changed")
		}
		clone = launch.Env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"]
		if filepath.Dir(filepath.Dir(clone)) != filepath.Join(data, "model-discovery") {
			t.Fatalf("clone outside AO data directory: %s", clone)
		}
		info, err := os.Stat(clone)
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
			t.Fatalf("private clone = %v, err = %v", info, err)
		}
		var settings map[string]json.RawMessage
		raw, err := os.ReadFile(clone)
		if err != nil || json.Unmarshal(raw, &settings) != nil {
			t.Fatalf("read clone: %v", err)
		}
		var source map[string]json.RawMessage
		if err := json.Unmarshal(original, &source); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"model", "security", "policy"} {
			if !reflect.DeepEqual(settings[key], source[key]) {
				t.Fatalf("setting %s changed: %s", key, settings[key])
			}
		}
		for key, want := range map[string]string{
			"experimental": `{"autoMemory":false,"other":true}`,
			"hooksConfig":  `{"enabled":false,"disabled":["old"]}`,
			"admin":        `{"mcp":{"enabled":false,"config":{"server":"preserved"}},"extensions":{"enabled":false,"other":"preserved"},"other":"preserved"}`,
		} {
			actual, err := canonicalGeminiTestJSON(settings[key])
			expected, expectedErr := canonicalGeminiTestJSON([]byte(want))
			if err != nil || expectedErr != nil || actual != expected {
				t.Fatalf("preserved %s = %s, want %s", key, actual, expected)
			}
		}
		return want, nil
	})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("discovery = %#v, %v", got, err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatalf("clone not removed: %v", err)
	}
	actual, err := os.ReadFile(system)
	if err != nil || !reflect.DeepEqual(actual, original) {
		t.Fatal("original settings changed")
	}
	if request.Env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"] != system {
		t.Fatal("request environment mutated")
	}
}

func TestGeminiNativeDiscoveryFailsClosedWhenConfigurationCannotBePreserved(t *testing.T) {
	for _, raw := range []string{"null", "[]", "{", `{"experimental":null}`, `{"hooksConfig":true}`, `{"admin":{"mcp":[]}}`} {
		t.Run(raw, func(t *testing.T) {
			data := t.TempDir()
			system := filepath.Join(data, "settings.json")
			writeConfig(t, system, raw)
			_, err := discoverGeminiOptions(context.Background(), ports.AgentModelDiscoveryRequest{WorkingDir: data, Env: map[string]string{"AO_DATA_DIR": data, "GEMINI_CLI_SYSTEM_SETTINGS_PATH": system}}, nil, func(context.Context, acpdriver.Launch, string, *slog.Logger) ([]ports.ChatConfigOption, error) {
				t.Fatal("probe started with unpreserved settings")
				return nil, nil
			})
			if err == nil {
				t.Fatal("expected preservation error")
			}
		})
	}
}

func TestGeminiNativeDiscoveryMissingSystemSettingsAndProbeFailure(t *testing.T) {
	data := t.TempDir()
	defaults := filepath.Join(data, "explicit-defaults.json")
	request := ports.AgentModelDiscoveryRequest{WorkingDir: data, Env: map[string]string{
		"AO_DATA_DIR": data, "GEMINI_CLI_SYSTEM_SETTINGS_PATH": filepath.Join(data, "missing.json"), "GEMINI_CLI_SYSTEM_DEFAULTS_PATH": defaults,
	}}
	want := errors.New("native discovery failed")
	var clone string
	_, err := discoverGeminiOptions(context.Background(), request, nil, func(_ context.Context, launch acpdriver.Launch, _ string, _ *slog.Logger) ([]ports.ChatConfigOption, error) {
		clone = launch.Env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"]
		if launch.Env["GEMINI_CLI_SYSTEM_DEFAULTS_PATH"] != defaults {
			t.Fatal("explicit defaults path changed")
		}
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatalf("failed probe clone not removed: %v", err)
	}
}

func TestGeminiNativeDiscoveryCanceledBeforeStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := discoverGeminiOptions(ctx, ports.AgentModelDiscoveryRequest{}, nil, func(context.Context, acpdriver.Launch, string, *slog.Logger) ([]ports.ChatConfigOption, error) {
		t.Fatal("canceled probe started")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestGeminiNativeDiscoveryRejectsUnreadableOrOversizedSystemSettings(t *testing.T) {
	data := t.TempDir()
	oversized := filepath.Join(data, "oversized.json")
	writeConfig(t, oversized, strings.Repeat(" ", modelConfigReadLimit+1))
	for _, source := range []string{data, oversized} {
		_, err := discoverGeminiOptions(context.Background(), ports.AgentModelDiscoveryRequest{WorkingDir: data, Env: map[string]string{"AO_DATA_DIR": data, "GEMINI_CLI_SYSTEM_SETTINGS_PATH": source}}, nil, func(context.Context, acpdriver.Launch, string, *slog.Logger) ([]ports.ChatConfigOption, error) {
			t.Fatal("probe started with unreadable settings")
			return nil, nil
		})
		if err == nil {
			t.Fatal("expected preservation error")
		}
	}
}

func canonicalGeminiTestJSON(raw []byte) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}
