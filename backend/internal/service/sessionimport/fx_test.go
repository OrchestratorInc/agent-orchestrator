package sessionimport

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

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const fxImportID = "synthetic521"

func writeFXImport(t *testing.T, root, cwd string, v2 bool) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", fxImportID)
	if v2 {
		return writeFile(t, filepath.Join(root, "sessions", "v2", fxImportID, "log.jsonl"), `{"v":1,"seq":1,"ts":1793600000000,"kind":"session_created","id":"`+fxImportID+`","workspace":"`+filepath.ToSlash(cwd)+`","role":"root"}
{"v":1,"seq":2,"ts":1793600000001,"kind":"item","type":"user","data":{"text":"# AGENTS.md instructions injected context"}}
{"v":1,"seq":3,"ts":1793600000002,"kind":"item","type":"user","data":{"text":"explain synthetic parser"}}
{"v":1,"seq":4,"ts":1793600000003,"kind":"item","type":"tool_result","data":{"text":"private tool output"}}
{"v":1,"seq":5,"ts":1793600000004,"kind":"item","type":"assistant","data":{"text":"synthetic answer"}}
{torn`)
	}
	writeFile(t, filepath.Join(dir, "session.json"), `{"schema_version":2,"id":"`+fxImportID+`","workspace_root":"`+filepath.ToSlash(cwd)+`","updated_at_ms":1793600000000}`)
	return writeFile(t, filepath.Join(dir, "events.jsonl"), `{"schema_version":1,"timestamp_ms":1793600000001,"event":{"user":{"text":"# AGENTS.md instructions injected context"}}}
{"schema_version":1,"timestamp_ms":1793600000002,"event":{"user":{"text":"explain synthetic parser"}}}
{"schema_version":1,"timestamp_ms":1793600000003,"event":{"tool_result":{"text":"private tool output"}}}
{"schema_version":1,"timestamp_ms":1793600000004,"event":{"assistant":{"text":"synthetic answer"}}}
{torn`)
}

func TestFXImportLayoutsAndTerminalMode(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		name := "v1"
		if v2 {
			name = "v2"
		}
		t.Run(name, func(t *testing.T) {
			root, cwd := t.TempDir(), t.TempDir()
			writeFXImport(t, root, cwd, v2)
			files, err := discoverFX(context.Background(), root)
			if err != nil || len(files) != 1 {
				t.Fatalf("discover = %#v, %v", files, err)
			}
			got, err := readFXTranscript(context.Background(), root, files[0].paths...)
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: explain synthetic parser", "assistant: synthetic answer")
			if got.nativeID != fxImportID || got.cwd != filepath.ToSlash(cwd) || got.title != "explain synthetic parser" || got.last.IsZero() {
				t.Fatalf("transcript = %#v", got)
			}
			store := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic-fx", Path: cwd}}}
			svc := New(store, "", nil)
			svc.now = func() time.Time { return time.UnixMilli(1793600000000).Add(24 * time.Hour) }
			svc.roots = map[domain.AgentHarness]string{domain.HarnessFX: root}
			preview, err := svc.Scan(context.Background())
			if err != nil || len(preview.Candidates) != 1 {
				t.Fatalf("scan = %#v, %v", preview, err)
			}
			if preview.Candidates[0].ProjectID != "synthetic-fx" {
				t.Fatalf("project = %q", preview.Candidates[0].ProjectID)
			}
			result, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
			if err != nil || len(result) != 1 || result[0].Status != "created" {
				t.Fatalf("import = %#v, %v", result, err)
			}
			if store.sessions[0].Mode != domain.SessionModeTUI || store.sessions[0].Metadata.AgentSessionID != fxImportID {
				t.Fatalf("record = %#v", store.sessions[0])
			}
			if err := svc.RefreshArchive(context.Background(), store.sessions[0]); err != nil {
				t.Fatalf("refresh imported archive = %v", err)
			}
			wantTexts(t, store.messages, "user: explain synthetic parser", "assistant: synthetic answer")
		})
	}
}

func TestFXDiscoveryPrefersConvertedConversation(t *testing.T) {
	root := t.TempDir()
	writeFXImport(t, root, t.TempDir(), false)
	path := writeFXImport(t, root, t.TempDir(), true)
	files, err := discoverFX(context.Background(), root)
	if err != nil || len(files) != 1 || len(files[0].paths) != 1 || files[0].paths[0] != path {
		t.Fatalf("discover = %#v, %v", files, err)
	}
}

func TestFXRefreshFindsConvertedConversation(t *testing.T) {
	for _, removeV1 := range []bool{false, true} {
		name := "retained-v1"
		if removeV1 {
			name = "removed-v1"
		}
		t.Run(name, func(t *testing.T) {
			root, cwd := t.TempDir(), t.TempDir()
			oldPath := writeFXImport(t, root, cwd, false)
			store := &importStore{projects: []domain.ProjectRecord{{ID: "fx-converted", Path: cwd}}}
			svc := New(store, "", nil)
			svc.now = func() time.Time { return time.UnixMilli(1793600000000).Add(24 * time.Hour) }
			svc.roots = map[domain.AgentHarness]string{domain.HarnessFX: root}
			preview, err := svc.Scan(context.Background())
			if err != nil || len(preview.Candidates) != 1 {
				t.Fatalf("scan = %#v, %v", preview, err)
			}
			result, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
			if err != nil || len(result) != 1 || result[0].Status != "created" {
				t.Fatalf("import = %#v, %v", result, err)
			}
			path := writeFXImport(t, root, cwd, true)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, strings.Replace(string(body), "synthetic answer", "new v2 answer", 1))
			wantLocator := canonical(oldPath)
			if removeV1 {
				if err := os.RemoveAll(filepath.Dir(oldPath)); err != nil {
					t.Fatal(err)
				}
			}
			if store.sessions[0].Metadata.NativeTranscriptPath != wantLocator {
				t.Fatal("fixture did not retain the imported v1 locator")
			}
			if err := svc.RefreshArchive(context.Background(), store.sessions[0]); err != nil {
				t.Fatal(err)
			}
			wantTexts(t, store.messages, "user: explain synthetic parser", "assistant: new v2 answer")
		})
	}
}

func TestFXImportRejectsInteriorCorruption(t *testing.T) {
	root := t.TempDir()
	path := writeFXImport(t, root, t.TempDir(), true)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The original malformed final record is tolerated, but not if followed
	// by a later record as it would be in a corrupt native journal.
	if _, err := readFXTranscript(context.Background(), root, path); err != nil {
		t.Fatalf("torn final record: %v", err)
	}
	writeFile(t, path, string(body)+"\n"+`{"kind":"item","type":"assistant","data":{"text":"after corruption"}}`)
	if _, err := readFXTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted a malformed interior record")
	}
}

func TestFXImportAcceptsOpaqueNonVisibleData(t *testing.T) {
	for _, data := range []string{`[1,2]`, `"opaque"`, `true`, `null`} {
		t.Run(data, func(t *testing.T) {
			root := t.TempDir()
			path := writeFXImport(t, root, t.TempDir(), true)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			opaque := `{"kind":"child_finished","child":"synthetic-child","work_id":"synthetic-work","outcome":"cancelled","data":` + data + "}\n"
			writeFile(t, path, strings.Replace(string(body), `{"v":1,"seq":4`, opaque+`{"v":1,"seq":4`, 1))
			got, err := readFXTranscript(context.Background(), root, path)
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: explain synthetic parser", "assistant: synthetic answer")
		})
	}
}

func TestFXImportMessageBlobs(t *testing.T) {
	for _, failure := range []string{"", "root-alias", "invalid-hash", "missing", "digest", "oversized", "symlink"} {
		name := failure
		if name == "" {
			name = "visible-messages"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if failure == "root-alias" {
				alias := filepath.Join(t.TempDir(), "fx-home")
				if err := os.Symlink(root, alias); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				root = alias
			}
			path := writeFXImport(t, root, t.TempDir(), true)
			text := strings.Repeat("blob message ", 24000)
			payload, err := json.Marshal(map[string]string{"text": text})
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(payload)
			hash := hex.EncodeToString(digest[:])
			blob := writeFile(t, filepath.Join(filepath.Dir(path), "blobs", hash), string(payload))
			switch failure {
			case "invalid-hash":
				hash = "../outside"
			case "missing":
				if err := os.Remove(blob); err != nil {
					t.Fatal(err)
				}
			case "digest":
				writeFile(t, blob, `{"text":"altered"}`)
			case "oversized":
				if err := os.Truncate(blob, maxVisibleBytes+1); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(blob); err != nil {
					t.Fatal(err)
				}
				outside := writeFile(t, filepath.Join(t.TempDir(), "outside"), string(payload))
				if err := os.Symlink(outside, blob); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			updated := strings.Replace(string(body), `"text":"explain synthetic parser"`, `"$blob":"`+hash+`"`, 1)
			updated = strings.Replace(updated, `"text":"synthetic answer"`, `"$blob":"`+hash+`"`, 1)
			// Tool references are intentionally not opened, even if invalid.
			updated = strings.Replace(updated, `"text":"private tool output"`, `"$blob":"../ignored-tool"`, 1)
			writeFile(t, path, updated)
			got, err := readFXTranscript(context.Background(), root, path)
			if failure != "" && failure != "root-alias" {
				if err == nil {
					t.Fatal("accepted an unverified message blob")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: "+text, "assistant: "+text)
		})
	}
}

func TestFXScanRefusesV1ChildConversation(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := writeFXImport(t, root, cwd, false)
	metadata := filepath.Join(filepath.Dir(path), "session.json")
	body, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.TrimSuffix(string(body), "}") + `,"subagent_child":true}`)
	if err := os.WriteFile(metadata, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFXTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted a v1 subagent child as an independently resumable chat")
	}
	st := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic-fx-child", Path: cwd}}}
	svc := New(st, "", nil)
	svc.now = func() time.Time { return time.UnixMilli(1793600000000).Add(24 * time.Hour) }
	svc.roots = map[domain.AgentHarness]string{domain.HarnessFX: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 0 {
		t.Fatalf("child scan = %#v, %v", preview, err)
	}
}

func TestFXImportRejectsSymlinksAndMismatchedID(t *testing.T) {
	root := t.TempDir()
	path := writeFXImport(t, root, t.TempDir(), true)
	if err := os.WriteFile(path, []byte(`{"kind":"session_created","id":"another-id","workspace":"/work","role":"root"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFXTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted a mismatched native id")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := writeFile(t, filepath.Join(t.TempDir(), "outside.jsonl"), "{}\n")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	files, err := discoverFX(context.Background(), root)
	if err != nil || len(files) != 0 {
		t.Fatalf("symlink discover = %#v, %v", files, err)
	}
}

func TestFXRootOverrideAndOversizeToolResult(t *testing.T) {
	t.Setenv("FX_HOME", "/synthetic/fx")
	if fxRoot("/home/test") != "/synthetic/fx" {
		t.Fatal("FX_HOME ignored")
	}
	t.Setenv("FX_HOME", "")
	if fxRoot("/home/test") != filepath.Join("/home/test", ".fx") {
		t.Fatal("wrong default root")
	}
	old := maxRecordBytes
	maxRecordBytes = 512
	t.Cleanup(func() { maxRecordBytes = old })
	root := t.TempDir()
	path := writeFXImport(t, root, t.TempDir(), true)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "private tool output", strings.Repeat("x", 4096), 1))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readFXTranscript(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: explain synthetic parser", "assistant: synthetic answer")
}
