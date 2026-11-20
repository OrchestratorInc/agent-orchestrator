package deepseekharness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const importTestID = "00000000-0000-4000-8000-000000000522"

func importTestEvent(seq int, kind string, data any, surface any) []byte {
	e := map[string]any{"type": kind, "seq": seq, "time": 1790000000000 + seq, "data": data}
	if surface != nil {
		e["surfaceOp"] = surface
	}
	b, _ := json.Marshal(e)
	return append(b, '\n')
}

func importTestUser(text, kind string) map[string]any {
	return map[string]any{"id": text, "role": "user", "source": map[string]string{"kind": kind}, "content": []map[string]string{{"type": "text", "text": text}}}
}

func importFixture(t *testing.T, compressed bool) (root, path, cwd string) {
	t.Helper()
	root, cwd = t.TempDir(), t.TempDir()
	var err error
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(map[string]any{"type": "session", "version": 4, "id": importTestID, "cwd": cwd, "createdAt": 1790000000000, "isSeeded": false, "delegationDepth": 0})
	if err != nil {
		t.Fatal(err)
	}
	header = append(header, '\n')
	body := importTestEvent(0, "user/message", importTestUser("opening marker", "user"), "append")
	body = append(body, importTestEvent(1, "user/message", importTestUser("hidden runtime", "runtime-context"), "append")...)
	body = append(body, importTestEvent(2, "assistant/message", map[string]any{"message": map[string]any{"role": "assistant", "content": []map[string]string{{"type": "reasoning", "text": "hidden thoughts"}, {"type": "text", "text": "remembered"}}}}, "append")...)
	body = append(body, importTestEvent(3, "turn/end", map[string]any{}, nil)...)
	name := "session.v4.jsonl"
	data := append(header, body...)
	if compressed {
		name += ".zstd"
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderCRC(true))
		if err != nil {
			t.Fatal(err)
		}
		defer enc.Close()
		// Native storage uses one header frame and later event-batch frames.
		data = append(enc.EncodeAll(header, nil), enc.EncodeAll(body, nil)...)
	}
	path = filepath.Join(root, "sessions", importProjectKey(cwd), importTestID, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, path, cwd
}

func TestImportReadsAllCompressedFramesWithoutChangingSource(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		root, path, cwd := importFixture(t, compressed)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		a, err := ReadImportArchive(context.Background(), root, path)
		if err != nil {
			t.Fatal(err)
		}
		if a.ID != importTestID || a.CWD != cwd || len(a.Messages) != 2 || a.Messages[0].Text != "opening marker" || a.Messages[1].Text != "remembered" || a.Fingerprint == "" {
			t.Fatalf("archive=%+v", a)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("source changed: %v", err)
		}
	}
}

func TestImportFoldsSurfaceReplacementsAndHidesRuntimeContext(t *testing.T) {
	root, path, _ := importFixture(t, false)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{
		importTestEvent(4, "user/message", importTestUser("new runtime", "runtime-context"), map[string]any{"op": "replace", "startSeq": 1, "endSeq": 1}),
		importTestEvent(5, "user/message", importTestUser("edited opening", "user"), map[string]any{"op": "replace", "startSeq": 0, "endSeq": 0}),
	} {
		if _, err := f.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := ReadImportArchive(context.Background(), root, path)
	if err != nil || len(a.Messages) != 2 || a.Messages[0].Text != "edited opening" || a.Messages[1].Text != "remembered" {
		t.Fatalf("replacement=%+v,%v", a, err)
	}
}

func TestImportRefusesUnknownOrIncompleteHistory(t *testing.T) {
	for _, mutation := range []string{"version", "identity", "seeded", "parent", "missing header", "sequence", "empty", "attachment", "child", "torn", "new generation", "competing encoding"} {
		t.Run(mutation, func(t *testing.T) {
			root, path, _ := importFixture(t, false)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			h, rest, _ := bytes.Cut(b, []byte("\n"))
			var header map[string]any
			if err := json.Unmarshal(h, &header); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "version":
				header["version"] = 5
			case "identity":
				header["id"] = "wrong"
			case "seeded":
				header["isSeeded"] = true
			case "parent":
				header["parentSession"] = "outside"
			case "missing header":
				delete(header, "delegationDepth")
			case "sequence":
				rest = bytes.Replace(rest, []byte(`"seq":2`), []byte(`"seq":7`), 1)
			case "empty":
				rest = nil
			case "attachment":
				rest = append(rest, importTestEvent(4, "tool/result", map[string]string{"attachmentId": "outside"}, nil)...)
			case "child":
				rest = append(rest, importTestEvent(4, "subagent/catalog", map[string]string{"id": "outside"}, nil)...)
			case "torn":
				rest = rest[:len(rest)-1]
			case "new generation", "competing encoding":
				name := "session.v5.jsonl"
				if mutation == "competing encoding" {
					name = "session.v4.jsonl.zstd"
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), []byte("unknown"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h, err = json.Marshal(header)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(append(h, '\n'), rest...), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadImportArchive(context.Background(), root, path); err == nil {
				t.Fatal("accepted unsupported native history")
			}
		})
	}
}

func TestImportRejectsTornCompressedEventFrame(t *testing.T) {
	root, path, _ := importFixture(t, true)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b[:len(b)-3], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadImportArchive(context.Background(), root, path); err == nil {
		t.Fatal("accepted incomplete compressed event frame")
	}
}

func TestPrivateImportRemapsOnlyHeaderAndReusesLaterTurns(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t, true)
	if err := os.WriteFile(filepath.Join(root, ".credentials.yaml"), []byte("version: 1\nrefs:\n  DEEPSEEK_API_KEY: fake-native-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := ReadImportArchive(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint, Chat: true}
	ref, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := ReadImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath)
	if err != nil || copy.ID != a.ID || copy.CWD == cwd || !bytes.Equal(copy.body, a.body) {
		t.Fatalf("private native history=%+v,%v", copy, err)
	}
	if _, err := os.Stat(filepath.Join(ref.ConfigDir, ".credentials.yaml")); !os.IsNotExist(err) {
		t.Fatal("copied native credentials")
	}
	f, err := os.OpenFile(ref.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(importTestEvent(4, "user/message", importTestUser("later AO turn", "user"), "append")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := ReadImportArchive(ctx, root, path)
	if err != nil || after.Fingerprint != a.Fingerprint {
		t.Fatalf("source changed: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	retry, err := p.PrepareImportedSession(ctx, r)
	if err != nil || retry != ref {
		t.Fatalf("retry=%+v,%v", retry, err)
	}
	copy, err = ReadImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath)
	if err != nil || len(copy.Messages) != 3 || copy.Messages[2].Text != "later AO turn" {
		t.Fatalf("lost later turn: %+v,%v", copy, err)
	}
	if err := p.PrepareRuntimeLaunch(ctx, ports.WorkspaceHookConfig{ImportConfigDir: ref.ConfigDir, NativeSessionID: ref.NativeSessionID, WorkspacePath: r.LaunchCWD, Env: map[string]string{"DSH_HOME": ref.ConfigDir}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareImportedSession(ctx, r); err == nil {
		t.Fatal("missing prepared history silently replaced")
	}
	if _, _, err := p.GetRestoreCommand(ctx, ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: r.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyImportSourceNativeID: importTestID}}, Prompt: "continue"}); err == nil {
		t.Fatal("import missing native ID allowed a fresh terminal fallback")
	}
}

func TestImportRefusesUnknownCLIAndCredentialMigration(t *testing.T) {
	root, path, cwd := importFixture(t, false)
	a, err := ReadImportArchive(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return "0.3.0", nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint}
	if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
		t.Fatal("accepted unknown CLI")
	}
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	credential := filepath.Join(root, ".credentials.yaml")
	content := []byte("DEEPSEEK_API_KEY: SENSITIVE-FAKE-VALUE\n")
	if err := os.WriteFile(credential, content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = p.PrepareImportedSession(context.Background(), r)
	if err == nil || strings.Contains(err.Error(), "SENSITIVE") {
		t.Fatalf("credential migration error=%v", err)
	}
	after, err := os.ReadFile(credential)
	if err != nil || sha256.Sum256(after) != sha256.Sum256(content) {
		t.Fatal("original credential store modified")
	}
	availability, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: importTestID})
	if err != nil || availability != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("ordinary session probe=%v,%v", availability, err)
	}
}
