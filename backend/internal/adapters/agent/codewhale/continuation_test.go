package codewhale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codewhalestore"
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

func TestOrdinaryCodeWhaleRestoreDoesNotUseImportEligibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"future schema", func(doc map[string]any) { doc["schema_version"] = 2 }},
		{"child conversation", func(doc map[string]any) { doc["metadata"].(map[string]any)["spawn_depth"] = 1 }},
		{"null messages", func(doc map[string]any) { doc["messages"] = nil }},
		{"large history", func(doc map[string]any) {
			doc["messages"] = []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", (16<<20)+1)}}}}
		}},
		{"moved workspace", func(doc map[string]any) {
			doc["metadata"].(map[string]any)["workspace"] = filepath.Join(t.TempDir(), "no-longer-present")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir, root, workspace := t.TempDir(), t.TempDir(), t.TempDir()
			path := writeRestoreSnapshot(t, root, workspace, restoreTestID, "")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(before, &doc); err != nil {
				t.Fatal(err)
			}
			tc.change(doc)
			body, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			runPath := filepath.Join(dataDir, "running.json")
			if err := runfile.Write(runPath, runfile.Info{PID: 42, Port: 43123, StartedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{codewhaleHomeEnv: root, "AO_RUNTIME_LAUNCH_ID": "ordinary", "AO_RUN_FILE": runPath}
			p := &Plugin{resolvedBinary: "/opt/codewhale"}
			cfg := ports.WorkspaceHookConfig{DataDir: dataDir, SessionID: "ordinary-ao", WorkspacePath: workspace, NativeSessionID: restoreTestID, Env: env}
			if err := p.PrepareRuntimeLaunch(t.Context(), cfg); err != nil {
				t.Fatalf("ordinary restore: %v", err)
			}
			want := filepath.Join(dataDir, "agent-runtime", adapterID, "sessions", "ordinary-ao", "launches", "ordinary", "runtime")
			if env[codewhaleRuntimeDirEnv] != want {
				t.Fatalf("ordinary runtime=%q want=%q", env[codewhaleRuntimeDirEnv], want)
			}
			if id, ok, err := p.ResolveNativeSessionID(t.Context(), ports.NativeSessionResolveConfig{DataDir: dataDir, SessionID: "ordinary-ao", LaunchID: "ordinary"}); err != nil || !ok || id != restoreTestID {
				t.Fatalf("ordinary identity=%q ok=%v err=%v", id, ok, err)
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != string(body) {
				t.Fatalf("restore changed history: %v", err)
			}
			// The same unsupported snapshot is still refused when import-bound,
			// or when an AO copy marker exists without its recorded binding.
			if _, err := p.nativeRestoreRuntimeDir(t.Context(), cfg, restoreTestID, root, t.TempDir()); err == nil {
				t.Fatal("import-bound unsupported history accepted")
			}
			doc["ao_import"] = map[string]any{"source_id": restoreTestID, "digest": "invalid"}
			marked, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, marked, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := p.nativeRestoreRuntimeDir(t.Context(), cfg, restoreTestID, "", t.TempDir()); err == nil {
				t.Fatal("import marker bypassed strict restore validation")
			}
		})
	}
}

func TestCodeWhaleImportMarkerRemainsStrictAfterUnsupportedMetadata(t *testing.T) {
	root, workspace, fresh := t.TempDir(), t.TempDir(), t.TempDir()
	path := writeRestoreSnapshot(t, root, workspace, restoreTestID, "")
	metadata, err := json.Marshal(map[string]any{"id": restoreTestID, "workspace": workspace, "runtime_store": 42})
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range []bool{false, true} {
		body := `{"metadata":` + string(metadata) + `,"messages":[]`
		if imported {
			body += `,"ao_import":{"source_id":"source","digest":"invalid"}`
		}
		body += `}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{codewhaleHomeEnv: root}}
		got, err := New().nativeRestoreRuntimeDir(t.Context(), cfg, restoreTestID, "", fresh)
		if imported {
			if err == nil {
				t.Fatal("metadata type error hid the later import marker")
			}
		} else if err != nil || got != fresh {
			t.Fatalf("ordinary fallback=%q err=%v", got, err)
		}
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

func TestLegacyNonImportedRestorePreparesNewRuntime(t *testing.T) {
	ctx := context.Background()
	dataDir, root, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	writeRestoreSnapshot(t, root, workspace, restoreTestID, "")
	runPath := filepath.Join(dataDir, "running.json")
	if err := runfile.Write(runPath, runfile.Info{PID: 42, Port: 43123, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{codewhaleHomeEnv: root, "AO_RUNTIME_LAUNCH_ID": "legacy-launch", "AO_RUN_FILE": runPath}
	p := &Plugin{resolvedBinary: "/opt/codewhale"}
	if err := p.PrepareRuntimeLaunch(ctx, ports.WorkspaceHookConfig{DataDir: dataDir, SessionID: "legacy-ao", WorkspacePath: workspace, NativeSessionID: restoreTestID, Env: env}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataDir, "agent-runtime", adapterID, "sessions", "legacy-ao", "launches", "legacy-launch", "runtime")
	if env[codewhaleRuntimeDirEnv] != want {
		t.Fatalf("runtime = %q; want %q", env[codewhaleRuntimeDirEnv], want)
	}
	id, ok, err := p.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: dataDir, SessionID: "legacy-ao", LaunchID: "legacy-launch"})
	if err != nil || !ok || id != restoreTestID {
		t.Fatalf("legacy identity = %q, %v, %v", id, ok, err)
	}
	argv, ok, err := p.GetRestoreCommand(ctx, ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}})
	if err != nil || !ok || argv[len(argv)-1] != restoreTestID {
		t.Fatalf("legacy restore = %v, %v, %v", argv, ok, err)
	}
}

func TestImportedCodeWhaleReopenAllowsNativeHistoryGrowth(t *testing.T) {
	root, sourceCWD, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	source := writeRestoreSnapshot(t, root, sourceCWD, restoreTestID, "")
	p := &Plugin{resolvedBinary: "/synthetic/codewhale"}
	prepared, err := p.PrepareImportedSession(t.Context(), ports.ImportedSessionRelocation{NativeSessionID: restoreTestID, ConfigDir: root, TranscriptPath: source, LaunchCWD: workspace})
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{codewhaleHomeEnv: root}}
	if got, err := p.nativeRestoreRuntimeDir(t.Context(), cfg, prepared.NativeSessionID, root, runtimeDir); err != nil || got != runtimeDir {
		t.Fatalf("first open = %q, %v", got, err)
	}
	// Native first save removes ao_import and binds the copied conversation.
	seedRuntimeOwner(t, runtimeDir)
	path := writeRestoreSnapshot(t, root, workspace, prepared.NativeSessionID, runtimeDir)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	messages := doc["messages"].([]any)
	for range 257 {
		messages = append(messages, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 64<<10)}}})
	}
	doc["messages"] = messages
	body, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codewhalestore.Read(t.Context(), root, path); err == nil {
		t.Fatal("source-admission budget was not exceeded")
	}
	if got, err := p.nativeRestoreRuntimeDir(t.Context(), cfg, prepared.NativeSessionID, root, t.TempDir()); err != nil || got != runtimeDir {
		t.Fatalf("grown imported restore = %q, %v", got, err)
	}
	ref := ports.NativeSessionRef{ConfigDir: root, NativeSessionID: prepared.NativeSessionID}
	if got, ok, err := p.LocateTranscript(t.Context(), ref); err != nil || !ok || got != path {
		t.Fatalf("grown locator = %q, %v, %v", got, ok, err)
	}
	if got, err := p.ProbeNativeSession(t.Context(), ref); err != nil || got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("grown probe = %q, %v", got, err)
	}
	restore := ports.RestoreConfig{Env: cfg.Env, Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: prepared.NativeSessionID, ports.MetadataKeyImportConfigDir: root}}}
	if argv, ok, err := p.GetRestoreCommand(t.Context(), restore); err != nil || !ok || argv[len(argv)-1] != prepared.NativeSessionID {
		t.Fatalf("grown command = %v, %v, %v", argv, ok, err)
	}
	for _, kind := range []string{"identity", "workspace", "owner", "override", "profile", "import marker", "unbound", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			env := map[string]string{codewhaleHomeEnv: root}
			invalid := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: env}
			saved := append([]byte(nil), body...)
			defer func() {
				if err := os.WriteFile(path, saved, 0o600); err != nil {
					t.Error(err)
				}
				seedRuntimeOwner(t, runtimeDir)
			}()
			switch kind {
			case "identity":
				doc["metadata"].(map[string]any)["id"] = restoreTestID
			case "workspace":
				invalid.WorkspacePath = t.TempDir()
			case "owner":
				if err := os.WriteFile(filepath.Join(runtimeDir, "owner.json"), []byte(`{"owner_id":"foreign"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "override":
				env[codewhaleRuntimeDirEnv] = t.TempDir()
			case "profile":
				env[codewhaleHomeEnv] = t.TempDir()
			case "import marker":
				doc["ao_import"] = map[string]any{"source_id": restoreTestID, "digest": "invalid"}
			case "unbound":
				delete(doc["metadata"].(map[string]any), "runtime_store")
			case "malformed":
				if err := os.WriteFile(path, append(saved, []byte(` {}`)...), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "identity" || kind == "import marker" || kind == "unbound" {
				changed, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, changed, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.nativeRestoreRuntimeDir(t.Context(), invalid, prepared.NativeSessionID, root, t.TempDir()); err == nil {
				t.Fatal("invalid adopted binding accepted")
			}
			if err := json.Unmarshal(saved, &doc); err != nil {
				t.Fatal(err)
			}
		})
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(body) {
		t.Fatalf("restore modified native history: %v", err)
	}
}
