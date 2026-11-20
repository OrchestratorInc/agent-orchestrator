package kimi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func kimiImportFixture(t *testing.T) ports.ImportedSessionRelocation {
	t.Helper()
	root := t.TempDir()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wire := writeKimiTranscript(t, root, "wd_source_000000000000", kimiTestSession)
	state := map[string]any{"id": kimiTestSession, "version": 2, "cwd": "/source", "agents": map[string]any{"main": map[string]any{"homedir": filepath.Dir(wire), "type": "main"}}, "custom": map[string]any{}}
	b, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(kimiSessionDirForWire(wire), "state.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	history := `{"type":"metadata","protocol_version":"1.5","created_at":1700000000000}` + "\n" + `{"type":"profile.bind","agentId":"main","modelAlias":"zai/glm","time":1700000000001}` + "\n" + `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"remember MARKER"}],"origin":{"kind":"user"}},"time":1700000000002}` + "\n"
	if err := os.WriteFile(wire, []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("default_model = 'zai/glm'\n[providers.zai]\ntype = 'openai'\nbase_url = 'https://provider.invalid/v1'\napi_key = 'synthetic-private-key'\n[models.'zai/glm']\nprovider = 'zai'\nmodel = 'glm'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ports.ImportedSessionRelocation{NativeSessionID: kimiTestSession, ConfigDir: root, TranscriptPath: wire, SourceCWD: "/source", LaunchCWD: cwd, Chat: true, DataDir: t.TempDir()}
}

func TestKimiPrivatePreparationAndRetry(t *testing.T) {
	r := kimiImportFixture(t)
	before, err := ReadImportedSession(t.Context(), r.ConfigDir, r.NativeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	r.SourceFingerprint = before.Fingerprint
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if ref.NativeSessionID != r.NativeSessionID || ref.ConfigDir == r.ConfigDir {
		t.Fatalf("not isolated: %#v", ref)
	}
	copied, err := ReadImportedSession(t.Context(), ref.ConfigDir, ref.NativeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if copied.CWD != r.LaunchCWD || string(copied.Wire) != string(before.Wire) {
		t.Fatal("copy lost history or retained source cwd")
	}
	var state struct {
		Agents map[string]struct {
			Home string `json:"homedir"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(copied.State, &state); err != nil {
		t.Fatal(err)
	}
	if state.Agents["main"].Home != filepath.Dir(copied.Path) {
		t.Fatal("main agent still uses original path")
	}
	if _, err := os.Stat(filepath.Join(ref.ConfigDir, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("preparer copied config")
	}
	if err := os.RemoveAll(r.ConfigDir); err != nil {
		t.Fatal(err)
	}
	again, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil || again != ref {
		t.Fatalf("failed-checkpoint retry: %#v %v", again, err)
	}
}

func TestKimiPreparationRefusesUnknownOrChangedState(t *testing.T) {
	for _, mode := range []string{"version", "selection", "state", "protocol", "subagent", "symlink", "empty"} {
		t.Run(mode, func(t *testing.T) {
			r := kimiImportFixture(t)
			version := "2.4.1"
			switch mode {
			case "version":
				version = "3.0.0"
			case "selection":
				r.SourceFingerprint = "kimi:v1:changed"
			case "state":
				p := filepath.Join(kimiSessionDirForWire(r.TranscriptPath), "state.json")
				b, _ := os.ReadFile(p)
				b = []byte(strings.Replace(string(b), `"version":2`, `"version":3`, 1))
				if err := os.WriteFile(p, b, 0o600); err != nil {
					t.Fatal(err)
				}
			case "protocol":
				b, _ := os.ReadFile(r.TranscriptPath)
				if err := os.WriteFile(r.TranscriptPath, []byte(strings.Replace(string(b), `"1.5"`, `"9.0"`, 1)), 0o600); err != nil {
					t.Fatal(err)
				}
			case "subagent":
				p := filepath.Join(kimiSessionDirForWire(r.TranscriptPath), "state.json")
				b, _ := os.ReadFile(p)
				var d map[string]any
				_ = json.Unmarshal(b, &d)
				d["agents"].(map[string]any)["child"] = map[string]any{"type": "subagent"}
				b, _ = json.Marshal(d)
				if err := os.WriteFile(p, b, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				p := r.TranscriptPath
				target := filepath.Join(t.TempDir(), "wire")
				if err := os.Rename(p, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					t.Skip(err)
				}
			case "empty":
				if err := os.WriteFile(r.TranscriptPath, []byte("{\"type\":\"metadata\",\"protocol_version\":\"1.5\"}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if ref, err := prepareKimiImport(context.Background(), r, version); err == nil || ref.NativeSessionID != "" {
				t.Fatalf("unsafe preparation: %#v %v", ref, err)
			}
		})
	}
}

func TestKimiImportedRuntimeDoesNotPersistKeys(t *testing.T) {
	r := kimiImportFixture(t)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	env, imported, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil)
	if err != nil || !imported || env["AO_KIMI_IMPORT_API_KEY"] != "synthetic-private-key" || env["KIMI_CODE_HOME"] != ref.ConfigDir {
		t.Fatalf("import runtime: imported=%v err=%v", imported, err)
	}
	if err := filepath.WalkDir(ref.ConfigDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if strings.Contains(string(b), "synthetic-private-key") {
				t.Fatal("key persisted in private home")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(ref.ConfigDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "api_key_env") || strings.Contains(string(cfg), "oauth") {
		t.Fatal("private config did not use process-only credential reference")
	}
	source, err := os.ReadFile(filepath.Join(r.ConfigDir, "config.toml"))
	if err != nil || !strings.Contains(string(source), "api_key = 'synthetic-private-key'") {
		t.Fatal("source configuration changed")
	}
}

func TestKimiPreparedCopyTamperingRefused(t *testing.T) {
	r := kimiImportFixture(t)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(ref.TranscriptPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("{\"type\":\"context.clear\"}\n")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareKimiImport(t.Context(), r, "2.4.1"); err == nil {
		t.Fatal("tampered native copy reused")
	}
}

func TestKimiRuntimeRefusesMissingProofAndOtherWorkspace(t *testing.T) {
	r := kimiImportFixture(t)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, "/other-worktree", "", nil); err == nil {
		t.Fatal("wrong workspace accepted")
	}
	if err := os.Remove(filepath.Join(ref.ConfigDir, ".ao-import.json")); err != nil {
		t.Fatal(err)
	}
	if _, imported, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil); !imported || err == nil {
		t.Fatal("missing private proof fell back to ordinary login")
	}
}

func TestKimiImportedTerminalHooksRefuseBeforeCredentialSeeding(t *testing.T) {
	dir := t.TempDir()
	err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{WorkspacePath: dir, NativeSessionID: kimiTestSession, ImportConfigDir: "/original"})
	if err == nil {
		t.Fatal("imported terminal hooks accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("refused terminal import wrote native state")
	}
}

func TestKimiImportWorkspaceHooksDoNotSeedSourceProfile(t *testing.T) {
	r := kimiImportFixture(t)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", r.ConfigDir)
	err = New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{WorkspacePath: r.LaunchCWD, DataDir: r.DataDir, Env: map[string]string{"KIMI_CODE_HOME": ref.ConfigDir}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ref.ConfigDir, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("workspace setup seeded profile/credentials")
	}
	if _, err := os.Stat(filepath.Join(ref.ConfigDir, "credentials")); !os.IsNotExist(err) {
		t.Fatal("workspace setup seeded credentials")
	}
	if _, _, err := New().GetRestoreCommand(t.Context(), ports.RestoreConfig{DataDir: r.DataDir, Env: map[string]string{"KIMI_CODE_HOME": ref.ConfigDir}}); err == nil {
		t.Fatal("private import entered terminal restore path")
	}
}

func TestKimiPrivateHomeLinkCannotRedirectRuntimeWrites(t *testing.T) {
	r := kimiImportFixture(t)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	aside := ref.ConfigDir + "-aside"
	if err := os.Rename(ref.ConfigDir, aside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(aside, ref.ConfigDir); err != nil {
		t.Skip(err)
	}
	if _, _, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil); err == nil {
		t.Fatal("linked private home accepted")
	}
	if _, err := prepareKimiImport(t.Context(), r, "2.4.1"); err == nil {
		t.Fatal("retry followed a linked private home")
	}
	if _, err := os.Stat(filepath.Join(aside, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("runtime wrote through private-home link")
	}
}
