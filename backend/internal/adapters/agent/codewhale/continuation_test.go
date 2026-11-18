package codewhale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

const restoreTestID = "88888888-8888-4888-8888-888888888888"

const runtimeOwnerID = "synthetic-runtime-owner"

func seedRuntimeOwner(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "owner.json"), []byte(`{"owner_id":"`+runtimeOwnerID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRestoreSnapshot(t *testing.T, root, workspace, id, runtimeDir string) string {
	t.Helper()
	metadata := map[string]any{"id": id, "workspace": workspace, "title": "Synthetic restart", "model": "synthetic-model", "created_at": "2026-11-03T00:00:00Z", "updated_at": "2026-11-03T00:00:00Z", "message_count": 1, "total_tokens": 0}
	if runtimeDir != "" {
		sum := sha256.Sum256([]byte(runtimeOwnerID + "\x00" + filepath.Join(runtimeDir, "events.lock")))
		metadata["runtime_store"] = map[string]any{"data_dir": runtimeDir, "execution_scope": hex.EncodeToString(sum[:])}
	}
	body, err := json.Marshal(map[string]any{
		"schema_version": 1, "metadata": metadata, "system_prompt": nil,
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "synthetic restart prompt"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeRestoreRuntimeDirKeepsFreshLaunchUnchanged(t *testing.T) {
	fresh := filepath.Join(t.TempDir(), "launches", "generation", "runtime")
	got, err := New().nativeRestoreRuntimeDir(context.Background(), ports.WorkspaceHookConfig{}, "", "", fresh)
	if err != nil || got != fresh {
		t.Fatalf("fresh runtime = %q, %v", got, err)
	}
}

func TestNativeRestoreRuntimeDirReusesExactSavedBinding(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	saved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedRuntimeOwner(t, saved)
	writeRestoreSnapshot(t, root, workspace, restoreTestID, saved)
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{codewhaleHomeEnv: root}}
	got, err := New().nativeRestoreRuntimeDir(context.Background(), cfg, restoreTestID, "", filepath.Join(t.TempDir(), "new-generation"))
	if err != nil || got != saved {
		t.Fatalf("restored runtime = %q, %v", got, err)
	}
}

func TestNativeRestoreResolvesSelectedProfile(t *testing.T) {
	for _, kind := range []string{"relative", "cleared", "tilde"} {
		t.Run(kind, func(t *testing.T) {
			workspace, home := t.TempDir(), t.TempDir()
			t.Setenv(codewhaleHomeEnv, t.TempDir())
			env := map[string]string{"HOME": home}
			root := filepath.Join(workspace, "profile")
			switch kind {
			case "relative":
				env[codewhaleHomeEnv] = "profile"
			case "cleared":
				env[codewhaleHomeEnv] = ""
				root = filepath.Join(home, ".codewhale")
			case "tilde":
				env[codewhaleHomeEnv] = "~/profile"
				root = filepath.Join(home, "profile")
			}
			saved, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			seedRuntimeOwner(t, saved)
			writeRestoreSnapshot(t, root, workspace, restoreTestID, saved)
			cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: env}
			got, err := New().nativeRestoreRuntimeDir(context.Background(), cfg, restoreTestID, root, t.TempDir())
			if err != nil || got != saved {
				t.Fatalf("selected profile runtime = %q, %v", got, err)
			}
		})
	}
}

func TestNativeRestoreAllowsBoundEmptyHistory(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	saved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedRuntimeOwner(t, saved)
	path := writeRestoreSnapshot(t, root, workspace, restoreTestID, saved)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	raw["messages"] = []any{}
	body, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{codewhaleHomeEnv: root}}
	got, err := New().nativeRestoreRuntimeDir(context.Background(), cfg, restoreTestID, "", t.TempDir())
	if err != nil || got != saved {
		t.Fatalf("empty native restart = %q, %v", got, err)
	}
}

func TestNativeRestoreRuntimeDirRefusesMissingAndMismatchedState(t *testing.T) {
	for _, kind := range []string{"unbound", "missing_directory", "workspace", "override", "identity", "owner", "profile"} {
		t.Run(kind, func(t *testing.T) {
			root, workspace := t.TempDir(), t.TempDir()
			saved, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			seedRuntimeOwner(t, saved)
			if kind == "unbound" {
				saved = ""
			}
			if kind == "missing_directory" {
				saved = filepath.Join(saved, "missing")
			}
			writeRestoreSnapshot(t, root, workspace, restoreTestID, saved)
			cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{codewhaleHomeEnv: root}}
			id := restoreTestID
			switch kind {
			case "workspace":
				cfg.WorkspacePath = t.TempDir()
			case "override":
				cfg.Env[codewhaleRuntimeDirEnv] = t.TempDir()
			case "identity":
				id = "99999999-9999-4999-8999-999999999999"
			case "owner":
				if err := os.WriteFile(filepath.Join(saved, "owner.json"), []byte(`{"owner_id":"different-owner"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "profile":
				cfg.Env[codewhaleHomeEnv] = t.TempDir()
			}
			if _, err := New().nativeRestoreRuntimeDir(context.Background(), cfg, id, root, t.TempDir()); err == nil {
				t.Fatal("selected new authority for an invalid native restore")
			}
		})
	}
}

func TestImportedFirstOpenThenSecondNativeRestart(t *testing.T) {
	root, sourceCWD, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := writeRestoreSnapshot(t, root, sourceCWD, restoreTestID, "")
	p := New()
	prepared, err := p.PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{NativeSessionID: restoreTestID, ConfigDir: root, TranscriptPath: source, SourceCWD: sourceCWD, LaunchCWD: launch})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: launch, Env: map[string]string{codewhaleHomeEnv: root}}
	first, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.nativeRestoreRuntimeDir(context.Background(), cfg, prepared.NativeSessionID, prepared.ConfigDir, first)
	if err != nil || got != first {
		t.Fatalf("imported first open = %q, %v", got, err)
	}
	// The native host saves this binding on the copied conversation's first
	// open. Its next generation must reuse it instead of selecting a new store.
	seedRuntimeOwner(t, first)
	writeRestoreSnapshot(t, root, launch, prepared.NativeSessionID, first)
	got, err = p.nativeRestoreRuntimeDir(context.Background(), cfg, prepared.NativeSessionID, prepared.ConfigDir, filepath.Join(t.TempDir(), "second-generation"))
	if err != nil || got != first {
		t.Fatalf("second native restart = %q, %v", got, err)
	}
	// ImportConfigDir remains set after adoption. It cannot authorize a later
	// missing binding just because this conversation originally came from import.
	writeRestoreSnapshot(t, root, launch, prepared.NativeSessionID, "")
	if _, err := p.nativeRestoreRuntimeDir(context.Background(), cfg, prepared.NativeSessionID, prepared.ConfigDir, t.TempDir()); err == nil {
		t.Fatal("recreated runtime authority after the native binding disappeared")
	}
}

func TestImportedFirstOpenRefusesUnprovenOrChangedCopy(t *testing.T) {
	for _, kind := range []string{"changed", "missing_proof", "wrong_source", "ordinary_restore", "missing_identity"} {
		t.Run(kind, func(t *testing.T) {
			root, sourceCWD, launch := t.TempDir(), t.TempDir(), t.TempDir()
			source := writeRestoreSnapshot(t, root, sourceCWD, restoreTestID, "")
			p := New()
			prepared, err := p.PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{NativeSessionID: restoreTestID, ConfigDir: root, TranscriptPath: source, LaunchCWD: launch})
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(prepared.TranscriptPath)
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			id, recordedRoot := prepared.NativeSessionID, prepared.ConfigDir
			switch kind {
			case "changed":
				raw["metadata"].(map[string]any)["title"] = "changed after copy"
			case "missing_proof":
				delete(raw, "ao_import")
			case "wrong_source":
				raw["ao_import"].(map[string]any)["source_id"] = "99999999-9999-4999-8999-999999999999"
			case "ordinary_restore":
				recordedRoot = ""
			case "missing_identity":
				id = ""
			}
			body, err = json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(prepared.TranscriptPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := ports.WorkspaceHookConfig{WorkspacePath: launch, Env: map[string]string{codewhaleHomeEnv: root}}
			if _, err := p.nativeRestoreRuntimeDir(context.Background(), cfg, id, recordedRoot, t.TempDir()); err == nil {
				t.Fatal("accepted unproven unbound native restore")
			}
		})
	}
}

func TestNativeRestoreLocatorSelectsExactConversationInSharedRuntime(t *testing.T) {
	dataDir, root, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	runtimeDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedRuntimeOwner(t, runtimeDir)
	writeRestoreSnapshot(t, root, workspace, restoreTestID, runtimeDir)
	writeRestoreSnapshot(t, root, workspace, "99999999-9999-4999-8999-999999999999", runtimeDir)
	runPath := filepath.Join(dataDir, "running.json")
	if err := runfile.Write(runPath, runfile.Info{PID: 42, Port: 43123, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{codewhaleHomeEnv: root, "AO_RUNTIME_LAUNCH_ID": "selected-launch", "AO_RUN_FILE": runPath}
	p := &Plugin{resolvedBinary: "/synthetic/codewhale"}
	if _, err := os.Stat(filepath.Dir(nativeSessionLocatorPath(dataDir, "ao-selected", "selected-launch"))); !os.IsNotExist(err) {
		t.Fatalf("new launch locator directory already exists: %v", err)
	}
	if err := p.PrepareRuntimeLaunch(context.Background(), ports.WorkspaceHookConfig{
		DataDir: dataDir, SessionID: "ao-selected", WorkspacePath: workspace, Env: env,
		NativeSessionID: restoreTestID,
	}); err != nil {
		t.Fatal(err)
	}
	if env[codewhaleRuntimeDirEnv] != runtimeDir {
		t.Fatalf("runtime = %q, want %q", env[codewhaleRuntimeDirEnv], runtimeDir)
	}
	id, ok, err := p.ResolveNativeSessionID(context.Background(), ports.NativeSessionResolveConfig{DataDir: dataDir, SessionID: "ao-selected", LaunchID: "selected-launch"})
	if err != nil || !ok || id != restoreTestID {
		t.Fatalf("selected native id = %q, %v, %v", id, ok, err)
	}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}},
	})
	if err != nil || !ok || cmd[len(cmd)-2] != "--resume" || cmd[len(cmd)-1] != restoreTestID {
		t.Fatalf("restore = %#v, %v, %v", cmd, ok, err)
	}
}
