package zcode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// This subprocess is a launch-boundary fixture, not a live ZCode qualification.
// Native user config and child tools must keep resolving the same home even
// when AO supplies a session ID and data directory.
func TestLaunchPreservesNativeHomeConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	files := map[string]string{
		".gitconfig":             "[user]\nname = Native User\n[credential]\nhelper = native-fixture\n",
		".zcode/cli/config.json": `{"model":"native-default"}`,
		".zcode/AGENTS.md":       "native user instructions",
	}
	for name, content := range files {
		path := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dataDir := t.TempDir()
	plugin := &Plugin{resolvedBinary: os.Args[0]}
	cfg := ports.LaunchConfig{DataDir: dataDir, SessionID: "native-home-boundary"}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "AO_SESSION_ID": cfg.SessionID}
	// Exercise the same optional environment hook used by the manager. A
	// reintroduced HOME overlay must fail this boundary regression.
	if augmenter, ok := any(plugin).(interface {
		AugmentRuntimeEnv(map[string]string, string)
	}); ok {
		augmenter.AugmentRuntimeEnv(env, dataDir)
	}
	argv, err := plugin.GetLaunchCommand(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), argv[0], append(argv[1:], "-test.run=^TestZCodeNativeHomeChild$")...) //nolint:gosec // Launch this test executable as the child-tool fixture.
	cmd.Env = append(os.Environ(), "AO_ZCODE_HOME_TEST_CHILD=1")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child tool: %v: %s", err, output)
	}
	var got struct {
		Home     string
		UserHome string
		Files    map[string]string
	}
	if err := json.NewDecoder(strings.NewReader(string(output))).Decode(&got); err != nil {
		t.Fatalf("child result: %v: %s", err, output)
	}
	if got.Home != home || got.UserHome != home || !reflect.DeepEqual(got.Files, files) {
		t.Fatalf("child resolved home/config = %#v; want home %q and original native files", got, home)
	}
	for _, source := range []string{"inline", "file"} {
		t.Run(source, func(t *testing.T) {
			withInstructions := cfg
			if source == "inline" {
				withInstructions.SystemPrompt = "AO standing instructions"
			} else {
				withInstructions.SystemPromptFile = filepath.Join(t.TempDir(), "instructions.md")
				if err := os.WriteFile(withInstructions.SystemPromptFile, []byte("AO standing instructions"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if argv, err := plugin.GetLaunchCommand(t.Context(), withInstructions); argv != nil || err == nil {
				t.Fatalf("instruction launch = %v, %v; want fail-closed rejection", argv, err)
			}
			for name, want := range files {
				got, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
				if err != nil || string(got) != want {
					t.Fatalf("native file %q changed: %q, %v", name, got, err)
				}
			}
		})
	}
	if entries, err := os.ReadDir(dataDir); err != nil || len(entries) != 0 {
		t.Fatalf("launch wrote session-home state: %v, %v", entries, err)
	}
}

func TestZCodeNativeHomeChild(t *testing.T) {
	if os.Getenv("AO_ZCODE_HOME_TEST_CHILD") != "1" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	for _, name := range []string{".gitconfig", ".zcode/cli/config.json", ".zcode/AGENTS.md"} {
		content, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(content)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Home     string
		UserHome string
		Files    map[string]string
	}{Home: os.Getenv("HOME"), UserHome: home, Files: files}); err != nil {
		t.Fatal(err)
	}
}
