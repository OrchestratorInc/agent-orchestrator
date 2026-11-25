package kimi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func kimiImportFixture(t *testing.T) ports.ImportedSessionRelocation {
	t.Helper()
	root := t.TempDir()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wire := writeKimiTranscript(t, root, "wd_source_000000000000", kimiTestSession)
	sourceCWD := filepath.Join(root, "source-workdir")
	state := map[string]any{"id": kimiTestSession, "version": 2, "cwd": sourceCWD, "agents": map[string]any{"main": map[string]any{"homedir": filepath.Dir(wire), "type": "main"}}, "custom": map[string]any{}}
	b, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(kimiSessionDirForWire(wire), "state.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	history := `{"type":"metadata","protocol_version":"1.5","created_at":1700000000000}` + "\n" + `{"type":"profile.bind","agentId":"main","modelAlias":"zai/glm","time":1700000000001}` + "\n" + `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"remember MARKER"}],"origin":{"kind":"user"}},"time":1700000000002}` + "\n"
	binding, _ := json.Marshal(map[string]string{"type": "runtime.set_binding", "agentId": "main", "runtimeId": "local", "workspaceId": kimiWorkdirKey(sourceCWD)})
	lines := strings.SplitN(history, "\n", 2)
	history = lines[0] + "\n" + string(binding) + "\n" + lines[1]
	if err := os.WriteFile(wire, []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("default_model = 'zai/glm'\n[providers.zai]\ntype = 'openai'\nbase_url = 'https://provider.invalid/v1'\napi_key = 'synthetic-private-key'\n[models.'zai/glm']\nprovider = 'zai'\nmodel = 'glm'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ports.ImportedSessionRelocation{NativeSessionID: kimiTestSession, ConfigDir: root, TranscriptPath: wire, SourceCWD: sourceCWD, LaunchCWD: cwd, Chat: true, DataDir: t.TempDir()}
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
	expected, err := remapKimiWire(before.Wire, r.LaunchCWD)
	if err != nil {
		t.Fatal(err)
	}
	if copied.CWD != r.LaunchCWD || string(copied.Wire) != string(expected) {
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

// A new selection must not be trapped by an older reservation, and it must
// never retire a home/proof that a prepared or active session may still own.
func TestKimiReimportChangedSelectionPreservesEarlierBinding(t *testing.T) {
	for _, mode := range []string{"prepared", "advanced private history", "missing private home", "without selection fingerprint", "legacy prepared", "legacy advanced private history", "legacy missing private home"} {
		t.Run(mode, func(t *testing.T) {
			r := kimiImportFixture(t)
			original, err := ReadImportedSession(t.Context(), r.ConfigDir, r.NativeSessionID)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "without selection fingerprint" {
				r.SourceFingerprint = original.Fingerprint
			}
			first, err := prepareKimiImport(t.Context(), r, "2.4.1")
			if err != nil {
				t.Fatal(err)
			}
			binding := r.ConfigDir + "\x00" + r.NativeSessionID + "\x00" + r.LaunchCWD
			reservationPath := filepath.Join(r.DataDir, "native-imports", "kimi", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding+"\x00"+original.Fingerprint))))
			if strings.HasPrefix(mode, "legacy") {
				legacyPath := filepath.Join(filepath.Dir(reservationPath), fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding))))
				if err := os.Rename(reservationPath, legacyPath); err != nil {
					t.Fatal(err)
				}
				reservationPath = legacyPath
			}
			reservation, err := os.ReadFile(reservationPath)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := os.ReadFile(filepath.Join(first.ConfigDir, ".ao-import.json"))
			if err != nil {
				t.Fatal(err)
			}
			appendTurn := func(path, marker string) {
				t.Helper()
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = fmt.Fprintf(f, `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":%q}],"origin":{"kind":"user"}}}`+"\n", marker)
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("append native turn: %v, %v", err, closeErr)
				}
			}
			if strings.Contains(mode, "advanced private history") {
				appendTurn(first.TranscriptPath, "PRIVATE_ONLY_TURN")
			}
			privateWire, err := os.ReadFile(first.TranscriptPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(mode, "missing private home") {
				if err := os.RemoveAll(first.ConfigDir); err != nil {
					t.Fatal(err)
				}
			}
			appendTurn(r.TranscriptPath, "NEW_SOURCE_TURN")
			selected, err := ReadImportedSession(t.Context(), r.ConfigDir, r.NativeSessionID)
			if err != nil {
				t.Fatal(err)
			}
			if selected.Fingerprint == original.Fingerprint {
				t.Fatal("new turn did not advance selection")
			}
			if strings.HasSuffix(mode, "prepared") {
				// A lost checkpoint still recovers its exact earlier selection.
				if retry, err := prepareKimiImport(t.Context(), r, "2.4.1"); err != nil || retry != first {
					t.Fatalf("earlier selection retry: %#v, %v", retry, err)
				}
			}
			if strings.Contains(mode, "advanced private history") || strings.Contains(mode, "missing private home") {
				if _, err := prepareKimiImport(t.Context(), r, "2.4.1"); err == nil {
					t.Fatal("retry replaced a changed/missing earlier binding")
				}
			}
			if mode != "without selection fingerprint" {
				r.SourceFingerprint = selected.Fingerprint
			}
			second, err := prepareKimiImport(t.Context(), r, "2.4.1")
			if err != nil || second.ConfigDir == first.ConfigDir || second.NativeSessionID != first.NativeSessionID {
				t.Fatalf("new selection did not get an independent home: %#v, %v", second, err)
			}
			copied, err := ReadImportedSession(t.Context(), second.ConfigDir, second.NativeSessionID)
			if err != nil || !bytes.Contains(copied.Wire, []byte("NEW_SOURCE_TURN")) || bytes.Contains(copied.Wire, []byte("PRIVATE_ONLY_TURN")) {
				t.Fatalf("new selection lost/mixed history: %v", err)
			}
			if retry, err := prepareKimiImport(t.Context(), r, "2.4.1"); err != nil || retry != second {
				t.Fatalf("new selection retry: %#v, %v", retry, err)
			}
			if strings.Contains(mode, "missing private home") {
				if _, err := os.Lstat(first.ConfigDir); !os.IsNotExist(err) {
					t.Fatalf("reimport recreated earlier home: %v", err)
				}
			} else {
				gotProof, err := os.ReadFile(filepath.Join(first.ConfigDir, ".ao-import.json"))
				if err != nil || !bytes.Equal(gotProof, proof) {
					t.Fatal("reimport rewrote earlier session proof")
				}
				gotWire, err := os.ReadFile(first.TranscriptPath)
				if err != nil || !bytes.Equal(gotWire, privateWire) {
					t.Fatal("reimport rewrote earlier private history")
				}
			}
			gotReservation, err := os.ReadFile(reservationPath)
			if err != nil || !bytes.Equal(gotReservation, reservation) {
				t.Fatal("reimport retired or rewrote earlier reservation")
			}
			unchanged, err := ReadImportedSession(t.Context(), r.ConfigDir, r.NativeSessionID)
			if err != nil || unchanged.Fingerprint != selected.Fingerprint {
				t.Fatal("reimport changed the source")
			}
		})
	}
}

func TestKimiPreparationRefusesUnknownOrChangedState(t *testing.T) {
	for _, mode := range []string{"version", "selection", "state", "protocol", "subagent", "symlink", "empty", "runtime"} {
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
			case "runtime":
				b, err := os.ReadFile(r.TranscriptPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(r.TranscriptPath, []byte(strings.Replace(string(b), `"runtimeId":"local"`, `"runtimeId":"remote"`, 1)), 0o600); err != nil {
					t.Fatal(err)
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

func kimiMediaFixture(t *testing.T, r ports.ImportedSessionRelocation) (string, []byte, []byte) {
	t.Helper()
	var pngData bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	blob := pngData.Bytes()
	encoded := base64.StdEncoding.EncodeToString(blob)
	if len(encoded) <= 4096 {
		t.Fatal("fixture must cross native media offload threshold")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(encoded)))
	wire, err := os.ReadFile(r.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	media := `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"describe this image"},{"type":"image_url","imageUrl":{"url":"blobref:image/png;` + hash + `"}}],"origin":{"kind":"user"}}}` + "\n"
	wire = append(wire, []byte(media)...)
	if err := os.WriteFile(r.TranscriptPath, wire, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(r.TranscriptPath), "blobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hash), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	return hash, blob, wire
}

func TestKimiPreparationPreservesVerifiedMediaAndRetry(t *testing.T) {
	r := kimiImportFixture(t)
	hash, blob, wire := kimiMediaFixture(t, r)
	if err := os.WriteFile(filepath.Join(filepath.Dir(r.TranscriptPath), "blobs", "unreferenced"), []byte("unrelated data"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := ReadImportedSession(t.Context(), r.ConfigDir, r.NativeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	r.SourceFingerprint = before.Fingerprint
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(filepath.Dir(ref.TranscriptPath), "blobs", hash)
	got, err := os.ReadFile(copyPath)
	if err != nil || !bytes.Equal(got, blob) {
		t.Fatalf("native image payload lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(copyPath), "unreferenced")); !os.IsNotExist(err) {
		t.Fatal("copied an unreferenced source blob")
	}
	after, err := os.ReadFile(r.TranscriptPath)
	if err != nil || !bytes.Equal(after, wire) {
		t.Fatal("copy changed the source journal")
	}
	afterBlob, err := os.ReadFile(filepath.Join(filepath.Dir(r.TranscriptPath), "blobs", hash))
	if err != nil || !bytes.Equal(afterBlob, blob) {
		t.Fatal("copy changed the source media")
	}
	if err := os.RemoveAll(r.ConfigDir); err != nil {
		t.Fatal(err)
	}
	if again, err := prepareKimiImport(t.Context(), r, "2.4.1"); err != nil || again != ref {
		t.Fatalf("media checkpoint retry: %#v %v", again, err)
	}
	if err := os.WriteFile(copyPath, []byte("tampered image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareKimiImport(t.Context(), r, "2.4.1"); err == nil {
		t.Fatal("retry accepted changed private media")
	}
}

func TestKimiPreparationRefusesInvalidMedia(t *testing.T) {
	for _, mode := range []string{"missing", "hash", "oversize", "symlink", "escape", "selection"} {
		t.Run(mode, func(t *testing.T) {
			r := kimiImportFixture(t)
			hash, blob, _ := kimiMediaFixture(t, r)
			path := filepath.Join(filepath.Dir(r.TranscriptPath), "blobs", hash)
			switch mode {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "hash":
				if err := os.WriteFile(path, []byte("changed image"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.Truncate(path, kimiBlobFileBudget+1); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "image")
				if err := os.WriteFile(target, blob, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip(err)
				}
			case "escape":
				wire, err := os.ReadFile(r.TranscriptPath)
				if err != nil {
					t.Fatal(err)
				}
				wire = bytes.ReplaceAll(wire, []byte(hash), []byte("../../outside"))
				if err := os.WriteFile(r.TranscriptPath, wire, 0o600); err != nil {
					t.Fatal(err)
				}
			case "selection":
				// A changed valid reference/payload changes the selection fingerprint.
				plain, err := os.ReadFile(r.TranscriptPath)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := ReadImportedSession(t.Context(), r.ConfigDir, r.NativeSessionID)
				if err != nil {
					t.Fatal(err)
				}
				r.SourceFingerprint = snapshot.Fingerprint
				newBlob := append(bytes.Clone(blob), 0)
				newHash := fmt.Sprintf("%x", sha256.Sum256([]byte(base64.StdEncoding.EncodeToString(newBlob))))
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), newHash), newBlob, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(r.TranscriptPath, bytes.ReplaceAll(plain, []byte(hash), []byte(newHash)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if ref, err := prepareKimiImport(t.Context(), r, "2.4.1"); err == nil || ref.NativeSessionID != "" {
				t.Fatalf("invalid media published: %#v %v", ref, err)
			}
			entries, err := os.ReadDir(filepath.Join(r.DataDir, "native-imports", "kimi"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("refused media left a copy/checkpoint: %v %v", entries, err)
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

func TestKimiImportedRuntimeRecognizesSessionScopedACPBinding(t *testing.T) {
	for _, mode := range []string{"session ACP", "other ACP session", "remote", "wrong workspace", "subagent"} {
		t.Run(mode, func(t *testing.T) {
			r := kimiImportFixture(t)
			ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
			if err != nil {
				t.Fatal(err)
			}
			binding := map[string]string{"type": "runtime.set_binding", "agentId": "main", "runtimeId": "acp:" + r.NativeSessionID, "workspaceId": kimiWorkdirKey(r.LaunchCWD)}
			switch mode {
			case "other ACP session":
				binding["runtimeId"] = "acp:session_00000000-0000-0000-0000-000000000000"
			case "remote":
				binding["runtimeId"] = "remote"
			case "wrong workspace":
				binding["workspaceId"] = kimiWorkdirKey(filepath.Join(r.LaunchCWD, "other"))
			case "subagent":
				binding["agentId"] = "child"
			}
			record, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(ref.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Write(append(record, '\n'))
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
			env, imported, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil)
			if !imported || (err == nil) != (mode == "session ACP") {
				t.Fatalf("native ACP binding validation: imported=%v error=%v", imported, err)
			}
			if err == nil && env["KIMI_CODE_HOME"] != ref.ConfigDir {
				t.Fatal("reopened ACP binding lost its private home")
			}
		})
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

func TestKimiRuntimeRemapPreservesOpaqueContext(t *testing.T) {
	r := kimiImportFixture(t)
	opaque := `{"type":"context.append_loop_event","event":{"type":"tool.result","result":{"workspaceId":"` + kimiWorkdirKey("/source") + `","cwd":"/source","marker":"KEEP","n":1e400}}}` + "\n"
	f, err := os.OpenFile(r.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(opaque)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	copied, err := ReadImportedSession(t.Context(), ref.ConfigDir, ref.NativeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(copied.Wire), opaque) {
		t.Fatal("opaque context rewritten")
	}
	for _, line := range strings.Split(string(copied.Wire), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["type"] == "runtime.set_binding" && record["workspaceId"] != kimiWorkdirKey(r.LaunchCWD) {
			t.Fatal("native replay still binds the source workspace")
		}
	}
}

func TestKimiRuntimeRejectsUnsupportedProviderAuthentication(t *testing.T) {
	for _, section := range []string{"custom_headers", "env"} {
		t.Run(section, func(t *testing.T) {
			r := kimiImportFixture(t)
			ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(r.ConfigDir, "config.toml")
			f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString("\n[providers.zai." + section + "]\nX_AUTH_TOKEN = 'synthetic-extra-key'\n")
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil); err == nil {
				t.Fatal("unsupported provider authentication accepted")
			}
			if _, err := os.Stat(filepath.Join(ref.ConfigDir, "config.toml")); !os.IsNotExist(err) {
				t.Fatal("rejected authentication wrote private config")
			}
		})
	}
}

func TestKimiRuntimeAcceptsWorkspaceAlias(t *testing.T) {
	r := kimiImportFixture(t)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "worktree")
	if err := os.Symlink(r.LaunchCWD, alias); err != nil {
		t.Skip(err)
	}
	if _, _, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, alias, "", nil); err != nil {
		t.Fatalf("workspace alias refused: %v", err)
	}
}

// Publishing the private home can succeed while saving ResumeNativeID fails.
// A Kimi update before the next open must not block recovering that home.
func TestKimiPreparedRecoverySurvivesCLIUpdate(t *testing.T) {
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
	updated := func(context.Context) error { return errors.New("kimi: native import requires Kimi Code 2.4.1") }
	again, err := prepareKimiImportChecked(t.Context(), r, updated)
	if err != nil || again != ref {
		t.Fatalf("prepared recovery after CLI update = %#v, %v", again, err)
	}
	r.DataDir = t.TempDir()
	if fresh, err := prepareKimiImportChecked(t.Context(), r, updated); err == nil || fresh.NativeSessionID != "" {
		t.Fatalf("new preparation bypassed version gate: %#v, %v", fresh, err)
	}
}
