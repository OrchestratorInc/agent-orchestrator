package freebuff

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Command preparation is also used for preflight. Only the guarded startup
// transaction may seed native preferences.
func TestLaunchNeverRewritesSharedPreferences(t *testing.T) {
	for _, input := range []string{"null", `{"freebuffModel":"deepseek/deepseek-v4-flash"}`, `{"freebuffModel":"openai/gpt-6-luna","freebuffCatalogReasoningEfforts":{"server-row":"low"}}`} {
		t.Run(input, func(t *testing.T) {
			dir := isolateConfig(t)
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			p := pluginWithBinary(t)
			for _, model := range []string{"z-ai/glm-5.3-flash", "deepseek/deepseek-v4-flash"} {
				if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: t.TempDir(), Config: ports.AgentConfig{Model: model, Effort: "high"}}); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != input {
				t.Fatalf("AO changed shared settings: %s", data)
			}
		})
	}
}

func readStartupSettings(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func startupConfig(session, dir, model string) ports.LaunchConfig {
	return ports.LaunchConfig{SessionID: session, Env: map[string]string{configDirEnv: dir}, Config: ports.AgentConfig{Model: model}}
}

func TestBeginStartupSeedsCanonicalModelAndNormalizesNull(t *testing.T) {
	for name, input := range map[string]string{
		"null":                      "null",
		"old default":               `{"freebuffModel":"deepseek/deepseek-v4-flash"}`,
		"existing user preferences": `{"mode":"DEFAULT","adsEnabled":false,"freebuffModelKey":"old-row","freebuffCatalogReasoningEfforts":{"unrelated-row":"low"},"freebuffReasoningEfforts":{"openai/gpt-6-luna":"xhigh"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			release, err := New().BeginStartup(context.Background(), startupConfig("session", dir, "deepseek/deepseek-v4-flash"))
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			settings := readStartupSettings(t, dir)
			if settings["freebuffModel"] != "deepseek/deepseek-v4-flash" || settings["freebuffModelDefaultMigration"] != defaultModelMigration {
				t.Fatalf("canonical pick was not made explicit: %v", settings)
			}
			if _, ok := settings["freebuffModelKey"]; ok {
				t.Fatal("old catalog selection outranks canonical startup seed")
			}
			if name == "existing user preferences" {
				if settings["adsEnabled"] != false || settings["mode"] != "DEFAULT" {
					t.Fatal("unrelated preferences changed")
				}
				if settings["freebuffCatalogReasoningEfforts"].(map[string]any)["unrelated-row"] != "low" || settings["freebuffReasoningEfforts"].(map[string]any)["openai/gpt-6-luna"] != "xhigh" {
					t.Fatal("other model efforts were changed")
				}
			}
		})
	}
}

func TestBeginStartupPreservesNewerMigrationStamp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"freebuffModelDefaultMigration":"future-native-migration"}`), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := New().BeginStartup(context.Background(), startupConfig("session", dir, "deepseek/deepseek-v4-flash"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := readStartupSettings(t, dir)["freebuffModelDefaultMigration"]; got != "future-native-migration" {
		t.Fatalf("migration stamp=%v", got)
	}
}

func TestBeginStartupUsesEffectiveProjectConfig(t *testing.T) {
	daemonDir := isolateConfig(t)
	path := filepath.Join(daemonDir, "settings.json")
	const original = `{"freebuffModel":"openai/gpt-6-luna"}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	release, err := New().BeginStartup(context.Background(), startupConfig("session", projectDir, "z-ai/glm-5.3-flash"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := readStartupSettings(t, projectDir)["freebuffModel"]; got != "z-ai/glm-5.3-flash" {
		t.Fatalf("project model=%v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("daemon preferences changed: %s err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "credentials.json")); !os.IsNotExist(err) {
		t.Fatal("startup must not copy or create credential profiles")
	}
}

func TestStartupGuardSerializesNativeInitialization(t *testing.T) {
	p, dir := New(), t.TempDir()
	releaseFirst, err := p.BeginStartup(context.Background(), startupConfig("first", dir, "z-ai/glm-5.3-flash"))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	started := make(chan struct{})
	acquired := make(chan func(), 1)
	failure := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		close(started)
		release, err := p.BeginStartup(ctx, startupConfig("second", dir, "openai/gpt-6-luna"))
		if err != nil {
			failure <- err
			return
		}
		acquired <- release
	}()
	<-started
	select {
	case release := <-acquired:
		release()
		t.Fatal("second launch overwrote the seed before native initialization completed")
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(30 * time.Millisecond):
	}
	if got := readStartupSettings(t, dir)["freebuffModel"]; got != "z-ai/glm-5.3-flash" {
		t.Fatalf("first native process would read %v", got)
	}
	releaseFirst()
	releaseFirst() // Manager cleanup may call a release more than once.
	select {
	case release := <-acquired:
		defer release()
	case err := <-failure:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got := readStartupSettings(t, dir)["freebuffModel"]; got != "openai/gpt-6-luna" {
		t.Fatalf("second native process would read %v", got)
	}
}

func TestStartupGuardCancellationAndSeedFailureRelease(t *testing.T) {
	p, dir := New(), t.TempDir()
	cfg := startupConfig("first", dir, "z-ai/glm-5.3-flash")
	release, err := p.BeginStartup(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := p.BeginStartup(ctx, startupConfig("cancelled", dir, "openai/gpt-6-luna")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked launch err=%v", err)
	}
	if got := readStartupSettings(t, dir)["freebuffModel"]; got != "z-ai/glm-5.3-flash" {
		t.Fatalf("cancelled launch changed model to %v", got)
	}
	release()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BeginStartup(context.Background(), cfg); err == nil {
		t.Fatal("malformed settings should fail without leaking the guard")
	}
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	retryCtx, retryCancel := context.WithTimeout(context.Background(), time.Second)
	defer retryCancel()
	release, err = p.BeginStartup(retryCtx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestConfigDirHonorsExplicitClearedOverride(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	t.Setenv(configDirEnv, t.TempDir())
	if got, want := configDir(map[string]string{configDirEnv: ""}), filepath.Join(base, ".config", "manicode"); got != want {
		t.Fatalf("configDir=%q want=%q", got, want)
	}
	if got := configDir(map[string]string{configDirEnv: "relative"}); got != "" {
		t.Fatalf("relative override resolved to %q", got)
	}
}
