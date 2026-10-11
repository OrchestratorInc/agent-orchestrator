package mastracode

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const testThread = "12345678-1234-1234-1234-123456789abc"
const emptyComposer = "╭────────────╮\n│ ›          │\n╰────────────╯\n▐ Mastra Code ▌"

func prepared(t *testing.T) (*Plugin, ports.WorkspaceHookConfig) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MASTRA_DB_PATH", "")
	t.Setenv("MASTRA_STORAGE_BACKEND", "")
	t.Setenv("MASTRA_DB_URL", "")
	p := New()
	cfg := ports.WorkspaceHookConfig{WorkspacePath: t.TempDir(), DataDir: t.TempDir(), SessionID: "mastra-1", Env: map[string]string{}, SystemPrompt: "PRIVATE-STANDING"}
	p.AugmentRuntimeEnv(cfg.Env, cfg.DataDir)
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return p, cfg
}

func history(t *testing.T, cfg ports.WorkspaceHookConfig) *sql.DB {
	t.Helper()
	state, err := readState(cfg.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state.Profile, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", state.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		`CREATE TABLE mastra_threads(id TEXT PRIMARY KEY, metadata BLOB)`,
		`CREATE TABLE mastra_messages(id TEXT PRIMARY KEY, thread_id TEXT, content TEXT)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	metadata, err := json.Marshal(map[string]any{"projectPath": cfg.WorkspacePath, "currentModelId": "deepseek/deepseek-chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mastra_threads VALUES (?,jsonb(?))`, testThread, string(metadata)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mastra_messages VALUES ('m1',?,'real turn')`, testThread); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPrivatePluginPreservesNativeConfigurationAndInstructions(t *testing.T) {
	p, cfg := prepared(t)
	agents := filepath.Join(cfg.WorkspacePath, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("USER-INSTRUCTIONS"), 0o600); err != nil {
		t.Fatal(err)
	}
	hooksPath := configPath(cfg.WorkspacePath, "hooks.json")
	if err := writeObject(hooksPath, map[string]any{"SessionStart": []any{map[string]any{"type": "command", "command": "user-hook"}}, "unknown": map[string]any{"keep": true}}); err != nil {
		t.Fatal(err)
	}
	registryPath := configPath(cfg.WorkspacePath, "plugins", "plugins.json")
	if err := writeObject(registryPath, map[string]any{"plugins": map[string]any{"user-plugin": map[string]any{"enabled": true, "path": "user"}}, "disabledPlugins": []string{"disabled-user"}, "unknown": "preserve"}); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "user-hook") != 1 || strings.Count(string(data), "ao-hook.cjs SessionStart") != 1 || !strings.Contains(string(data), "unknown") {
		t.Fatalf("native hooks lost/duplicated: %s", data)
	}
	data, err = os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"user-plugin", "disabled-user", "preserve"} {
		if !strings.Contains(string(data), value) {
			t.Fatalf("native plugins lost %s", value)
		}
	}
	data, err = os.ReadFile(configPath(cfg.WorkspacePath, "plugins", managedID, "index.ts"))
	if err != nil || !strings.Contains(string(data), "PRIVATE-STANDING") {
		t.Fatalf("standing instructions missing: %v", err)
	}
	data, err = os.ReadFile(agents)
	if err != nil || string(data) != "USER-INSTRUCTIONS" {
		t.Fatalf("AGENTS.md modified: %v", err)
	}
	if err := writeObject(registryPath, map[string]any{"disabledPlugins": []string{managedID}}); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("disabled native plugin was reenabled")
	}
}

func TestHistoryRejectsMissingForeignEmptyLockedAndUnsafeThreads(t *testing.T) {
	_, cfg := prepared(t)
	db := history(t, cfg)
	ctx := context.Background()
	check := func(id string) error {
		return validateHistory(ctx, cfg.WorkspacePath, id, "deepseek/deepseek-chat", ports.PermissionModeDefault)
	}
	if err := check(testThread); err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err := db.QueryRow(`SELECT metadata FROM mastra_threads WHERE id=?`, testThread).Scan(&original); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "session-init", "00000000-0000-0000-0000-000000000000"} {
		if err := check(id); err == nil {
			t.Fatalf("accepted missing history %q", id)
		}
	}
	for _, update := range []string{
		`UPDATE mastra_threads SET metadata=jsonb_set(metadata,'$.projectPath','/another-workspace')`,
		`UPDATE mastra_threads SET metadata=jsonb_set(metadata,'$.yolo',1)`,
		`UPDATE mastra_threads SET metadata=jsonb_set(metadata,'$.escapeAsCancel',0)`,
	} {
		// The adapter opens an independent read-only connection. Commit fixture
		// changes so that connection observes them, then restore the exact blob.
		if _, err := db.Exec(update); err != nil {
			t.Fatal(err)
		}
		if err := check(testThread); err == nil {
			t.Fatalf("accepted unsafe history: %s", update)
		}
		if _, err := db.Exec(`UPDATE mastra_threads SET metadata=? WHERE id=?`, original, testThread); err != nil {
			t.Fatal(err)
		}
	}
	state, err := readState(cfg.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state.Profile, "locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(state.Profile, "locks", testThread+".lock")
	if err := os.WriteFile(lock, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := check(testThread); err == nil {
		t.Fatal("accepted uncertain lock")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM mastra_messages`); err != nil {
		t.Fatal(err)
	}
	if err := check(testThread); err == nil {
		t.Fatal("accepted empty native history")
	}
}

func TestGenerationWitnessRejectsFallbackAndDraft(t *testing.T) {
	p, cfg := prepared(t)
	restore := ports.RestoreConfig{DataDir: cfg.DataDir, Session: ports.SessionRef{ID: cfg.SessionID, WorkspacePath: cfg.WorkspacePath, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: testThread}}}
	path := receiptPath(cfg.DataDir, cfg.SessionID, "g1")
	if ready, err := p.VerifyNativeRestore(context.Background(), restore, "g1", emptyComposer); ready || err != nil {
		t.Fatalf("missing runtime witness = %v,%v", ready, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, generation, screen string
		ready, wantErr         bool
	}{
		{testThread, "g1", emptyComposer, true, false},
		{"session-init", "g1", emptyComposer, false, true},
		{"00000000-0000-0000-0000-000000000000", "g1", emptyComposer, false, true},
		{testThread, "old", emptyComposer, false, true},
		{testThread, "g1", strings.Replace(emptyComposer, "› ", "› draft", 1), false, false},
	} {
		if err := writeObject(path, map[string]string{"nativeId": tc.id, "cwd": cfg.WorkspacePath, "generation": tc.generation}); err != nil {
			t.Fatal(err)
		}
		ready, err := p.VerifyNativeRestore(context.Background(), restore, "g1", tc.screen)
		if ready != tc.ready || (err != nil) != tc.wantErr {
			t.Fatalf("witness = %v,%v for %+v", ready, err, tc)
		}
	}
}

func TestComposerAdmissionAndUnsupportedPolicies(t *testing.T) {
	p := New()
	if err := p.ValidateTerminalMessage(emptyComposer, "-ordinary task"); err != nil {
		t.Fatal(err)
	}
	for _, screen := range []string{"", "old history", strings.Replace(emptyComposer, "› ", "› draft", 1), "Tool Approval Required\n" + emptyComposer} {
		if err := p.ValidateTerminalMessage(screen, "new task"); err == nil {
			t.Fatalf("unsafe screen accepted: %q", screen)
		}
	}
	for _, task := range []string{"/quit", " !rm file"} {
		if err := p.ValidateTerminalMessage(emptyComposer, task); err == nil {
			t.Fatal("native command admitted as a task")
		}
	}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAuto, ports.PermissionModeAcceptEdits} {
		if err := validatePolicy(mode, nil, nil); err == nil {
			t.Fatal("fabricated permission mapping")
		}
	}
	if got := p.Manifest().Capabilities; len(got) != 1 || got[0] != adapters.CapabilityAgent {
		t.Fatal("unexpected Chat/reviewer capability")
	}
}
