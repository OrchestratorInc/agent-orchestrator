package kimiacp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportedKimiRestoreAfterBinaryUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake native executable uses a Unix shell")
	}
	ctx := t.Context()
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "kimi")
	writeBinary := func(version string) {
		t.Helper()
		script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' '" + version + "'; exit; fi\n" +
			"[ \"$1\" = acp ] && [ -f \"$KIMI_CODE_HOME/.ao-import.json\" ] && [ \"$AO_KIMI_IMPORT_API_KEY\" = synthetic-key ]\n"
		if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeBinary("2.4.1")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	const id = "session_12345678-1234-4234-8234-123456789abc"
	sourceCWD := filepath.Join(t.TempDir(), "source")
	sum := sha256.Sum256([]byte(strings.ReplaceAll(sourceCWD, `\`, "/")))
	workspaceKey := fmt.Sprintf("wd_source_%x", sum[:6])
	sessionDir := filepath.Join(home, "sessions", workspaceKey, id)
	mainDir := filepath.Join(sessionDir, "agents", "main")
	if err := os.MkdirAll(mainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(map[string]any{"id": id, "version": 2, "cwd": sourceCWD, "agents": map[string]any{"main": map[string]string{"type": "main", "homedir": mainDir}}})
	if err != nil {
		t.Fatal(err)
	}
	wire := "{\"type\":\"metadata\",\"protocol_version\":\"1.5\"}\n" +
		fmt.Sprintf("{\"type\":\"runtime.set_binding\",\"agentId\":\"main\",\"runtimeId\":\"local\",\"workspaceId\":%q}\n", workspaceKey) +
		"{\"type\":\"context.append_message\",\"agentId\":\"main\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"remember marker\"}]}}\n"
	for path, data := range map[string][]byte{
		filepath.Join(sessionDir, "state.json"): state,
		filepath.Join(mainDir, "wire.jsonl"):    []byte(wire),
		filepath.Join(home, "config.toml"):      []byte("default_model = 'test/model'\n[providers.test]\ntype = 'openai'\nbase_url = 'https://provider.invalid/v1'\napi_key = 'synthetic-key'\n[models.'test/model']\nprovider = 'test'\nmodel = 'model'\n"),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := ports.ImportedSessionRelocation{NativeSessionID: id, ConfigDir: home, DataDir: t.TempDir(), LaunchCWD: t.TempDir(), Chat: true}
	plugin := kimi.New()
	ref, err := plugin.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	writeBinary("2.5.0")
	if _, err := plugin.PrepareImportedSession(ctx, r); err == nil || !strings.Contains(err.Error(), "requires Kimi Code 2.4.1") {
		t.Fatalf("unverified version accepted for preparation: %v", err)
	}
	cfg := acpdriver.LaunchConfig{WorkspacePath: r.LaunchCWD, DataDir: r.DataDir, ProviderConversationID: id, Env: map[string]string{"KIMI_CODE_HOME": ref.ConfigDir}}
	args, env, err := configure(ctx, cfg)
	if err != nil {
		t.Fatalf("updated binary could not configure imported chat: %v", err)
	}
	if len(args) != 1 || args[0] != "acp" || env["KIMI_CODE_HOME"] != ref.ConfigDir {
		t.Fatalf("lost native launch binding: %v", args)
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = r.LaunchCWD
	command.Env = []string{"KIMI_CODE_HOME=" + env["KIMI_CODE_HOME"], "AO_KIMI_IMPORT_API_KEY=" + env["AO_KIMI_IMPORT_API_KEY"]}
	if err := command.Run(); err != nil {
		t.Fatalf("updated binary could not launch imported ACP command: %v", err)
	}
	if err := os.Remove(ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := configure(ctx, cfg); err == nil {
		t.Fatal("missing private history accepted after binary update")
	}
}
