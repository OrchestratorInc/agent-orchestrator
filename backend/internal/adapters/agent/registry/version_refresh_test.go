package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Exercise every shipped adapter through its real native restore builder. No
// provider is installed or contacted: all resolution/probes use isolated CLIs.
func TestEveryRefreshHarnessResumesExactConversation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	binaries := map[string]string{"claude-code": "claude", "codex": "codex", "opencode": "opencode", "opencode-v2": "opencode", "grok": "grok", "cursor": "cursor-agent", "qwen": "qwen", "gemini": "gemini", "copilot": "copilot", "kimi": "kimi", "muse": "muse", "droid": "droid", "amp": "amp", "agy": "agy", "crush": "crush", "goose": "goose", "auggie": "auggie", "devin": "devin", "omp": "omp", "codewhale": "codewhale", "openhands": "openhands", "cline": "cline", "kiro": "kiro-cli", "kilocode": "kilo", "vibe": "vibe", "pi": "pi", "kimchi": "kimchi", "prime-agent": "prime-agent", "autohand": "autohand", "fx": "fx", "command-code": "cmd", "mimo-code": "mimo"}
	for _, harness := range agentregistry.Harnessed() {
		t.Run(string(harness.Harness), func(t *testing.T) {
			capability, ok := harness.Agent.(ports.AgentTerminalVersionRefresher)
			if !ok {
				t.Fatal("missing explicit refresh capability")
			}
			if !capability.SupportsTerminalVersionRefresh() {
				return
			}
			nativeID := "019e950e-52e0-7411-961b-d380ca7e610f"
			if string(harness.Harness) == "mimo-code" {
				nativeID = "ses_original"
			}
			dir := t.TempDir()
			home := t.TempDir()
			workspace := t.TempDir()
			cli := binaries[string(harness.Harness)]
			if cli == "" {
				t.Fatal("supported harness missing native command fixture")
			}
			binary := filepath.Join(dir, cli)
			version := "1.36.0"
			switch string(harness.Harness) {
			case "opencode":
				version = "1.2.27"
			case "opencode-v2":
				version = "2.0.0"
			case "mimo-code":
				version = "0.1.12"
			}
			label := cli
			switch string(harness.Harness) {
			case "muse":
				label = "Muse Code"
			case "openhands":
				label = "OpenHands CLI"
			}
			script := "#!/bin/sh\ncase \"$1\" in --version|-v|-V) echo '" + label + " " + version + "';; --help|help) printf 'Usage: goose [OPTIONS] COMMAND\\nsession Start sessions\\nrecipe Recipes\\n';; *) if [ \"$2\" = list ]; then printf '%s' '[{\"id\":\"" + nativeID + "\"}]'; else printf '%s' '{\"info\":{\"id\":\"" + nativeID + "\"}}'; fi;; esac\n"
			if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			promptFile := filepath.Join(t.TempDir(), "system.md")
			if err := os.WriteFile(promptFile, []byte("Keep code-word papaya"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := ports.RestoreConfig{SystemPromptFile: promptFile, Session: ports.SessionRef{ID: "ao-fixture", WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeID}}, DataDir: t.TempDir(), Config: ports.AgentConfig{Model: "provider/preserved-model"}, SystemPrompt: "Keep the original conversation and code-word papaya", Permissions: ports.PermissionModeDefault}
			argv, canResume, err := harness.Agent.GetRestoreCommand(context.Background(), cfg)
			if err != nil || !canResume {
				t.Fatalf("native restore=%t, %v", canResume, err)
			}
			if !slices.Contains(argv, nativeID) {
				t.Fatalf("restore does not target original identity: %q", argv)
			}
			if !slices.Contains(argv, binary) {
				t.Fatalf("restore escaped isolated executable: %q", argv)
			}
			for _, arg := range argv {
				if arg == "--fork" || arg == "--fork-session" || arg == "--continue" || arg == "--last" || arg == "--print" {
					t.Fatalf("passive resume starts another conversation/turn: %q", argv)
				}
			}
			jsonProbe := slices.Contains([]string{"opencode", "opencode-v2", "kimi", "kilocode", "mimo-code"}, string(harness.Harness))
			if probe, ok := harness.Agent.(ports.AgentInterfaceHandoffHistoryProbe); ok && jsonProbe {
				exists, err := probe.NativeConversationExists(context.Background(), cfg.Session, nativeID, nil)
				if err != nil || !exists {
					t.Fatalf("history preflight rejected exact native session: %t, %v", exists, err)
				}
				missingScript := strings.ReplaceAll(script, nativeID, "different-native-session")
				if err := os.WriteFile(binary, []byte(missingScript), 0755); err != nil {
					t.Fatal(err)
				}
				exists, err = probe.NativeConversationExists(context.Background(), cfg.Session, nativeID, nil)
				if exists {
					t.Fatalf("history preflight accepted missing native session: %v", err)
				}
			}
			cfg.Session.Metadata = nil
			cfg.Session.ID = ""
			if _, canResume, err := harness.Agent.GetRestoreCommand(context.Background(), cfg); err == nil && canResume {
				t.Fatal("missing native identity accepted")
			}
		})
	}
}
