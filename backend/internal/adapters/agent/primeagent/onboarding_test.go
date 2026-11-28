package primeagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func onboardingSettingsPath(dataDir string) string {
	return filepath.Join(dataDir, "agent-runtime", "prime-agent", "settings.json")
}

func writeOnboardingSettings(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readOnboardingSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, data)
	}
	return settings
}

func TestPreLaunchMarksOnboardingShownInFreshProfile(t *testing.T) {
	dataDir := t.TempDir()
	if err := New().PreLaunch(context.Background(), ports.LaunchConfig{DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	path := onboardingSettingsPath(dataDir)
	settings := readOnboardingSettings(t, path)
	if settings["onboardingShown"] != true || len(settings) != 1 {
		t.Fatalf("settings = %#v, want only onboardingShown: true", settings)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %o, want 600", info.Mode().Perm())
	}
}

func TestPreLaunchKeepsOtherSettings(t *testing.T) {
	for name, content := range map[string]string{
		"marker missing": `{"defaultModel":"lab/model","telemetry":{"noticeShown":true}}`,
		"marker false":   `{"defaultModel":"lab/model","telemetry":{"noticeShown":true},"onboardingShown":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := onboardingSettingsPath(dataDir)
			writeOnboardingSettings(t, path, content)
			if err := New().PreLaunch(context.Background(), ports.LaunchConfig{DataDir: dataDir}); err != nil {
				t.Fatal(err)
			}
			settings := readOnboardingSettings(t, path)
			if settings["onboardingShown"] != true {
				t.Fatalf("onboardingShown = %#v, want true", settings["onboardingShown"])
			}
			if settings["defaultModel"] != "lab/model" {
				t.Fatalf("defaultModel = %#v, want it kept", settings["defaultModel"])
			}
			telemetry, ok := settings["telemetry"].(map[string]any)
			if !ok || telemetry["noticeShown"] != true {
				t.Fatalf("telemetry = %#v, want it kept", settings["telemetry"])
			}
			if _, chosen := settings["agentTraces"]; chosen {
				t.Fatalf("settings = %#v, must not answer the trace question", settings)
			}
		})
	}
}

func TestPreLaunchLeavesCompletedOnboardingUntouched(t *testing.T) {
	dataDir := t.TempDir()
	path := onboardingSettingsPath(dataDir)
	const content = `{ "onboardingShown": true }`
	writeOnboardingSettings(t, path, content)
	oldTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := New().PreLaunch(context.Background(), ports.LaunchConfig{DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Fatalf("completed onboarding rewrote settings: modtime = %v, want %v", info.ModTime(), oldTime)
	}
}

func TestPreLaunchTreatsEmptySettingsAsFresh(t *testing.T) {
	dataDir := t.TempDir()
	path := onboardingSettingsPath(dataDir)
	writeOnboardingSettings(t, path, " \n")
	if err := New().PreLaunch(context.Background(), ports.LaunchConfig{DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	if settings := readOnboardingSettings(t, path); settings["onboardingShown"] != true {
		t.Fatalf("settings = %#v, want onboardingShown: true", settings)
	}
}

func TestPreLaunchReportsSettingsItCannotKeep(t *testing.T) {
	for name, content := range map[string]string{
		"malformed": `{"defaultModel":`,
		"array":     `["not", "an", "object"]`,
		"null":      `null`,
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := onboardingSettingsPath(dataDir)
			writeOnboardingSettings(t, path, content)
			if err := New().PreLaunch(context.Background(), ports.LaunchConfig{DataDir: dataDir}); err == nil {
				t.Fatal("expected an error for settings AO cannot round-trip")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != content {
				t.Fatalf("settings = %q, want them left as %q", data, content)
			}
		})
	}
}

func TestPreLaunchValidatesDataDirAndContext(t *testing.T) {
	p := New()
	if err := p.PreLaunch(context.Background(), ports.LaunchConfig{}); err == nil {
		t.Fatal("expected blank DataDir error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dataDir := t.TempDir()
	if err := p.PreLaunch(ctx, ports.LaunchConfig{DataDir: dataDir}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PreLaunch error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(onboardingSettingsPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled PreLaunch wrote settings: stat error = %v", err)
	}
}
