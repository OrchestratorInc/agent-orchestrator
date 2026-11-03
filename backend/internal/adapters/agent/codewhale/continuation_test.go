package codewhale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	got, err := New().nativeRestoreRuntimeDir(context.Background(), ports.WorkspaceHookConfig{}, "", "", false, fresh)
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
	got, err := New().nativeRestoreRuntimeDir(context.Background(), cfg, restoreTestID, "", false, filepath.Join(t.TempDir(), "new-generation"))
	if err != nil || got != saved {
		t.Fatalf("restored runtime = %q, %v", got, err)
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
			if _, err := New().nativeRestoreRuntimeDir(context.Background(), cfg, id, root, false, t.TempDir()); err == nil {
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
	got, err := p.nativeRestoreRuntimeDir(context.Background(), cfg, prepared.NativeSessionID, prepared.ConfigDir, true, first)
	if err != nil || got != first {
		t.Fatalf("imported first open = %q, %v", got, err)
	}
	// The native host saves this binding on the copied conversation's first
	// open. Its next generation must reuse it instead of selecting a new store.
	seedRuntimeOwner(t, first)
	writeRestoreSnapshot(t, root, launch, prepared.NativeSessionID, first)
	got, err = p.nativeRestoreRuntimeDir(context.Background(), cfg, prepared.NativeSessionID, prepared.ConfigDir, false, filepath.Join(t.TempDir(), "second-generation"))
	if err != nil || got != first {
		t.Fatalf("second native restart = %q, %v", got, err)
	}
}
