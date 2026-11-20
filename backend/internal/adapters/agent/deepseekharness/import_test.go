package deepseekharness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

func TestImportShorteningPreservesOpeningUserAfterAssistant(t *testing.T) {
	large := strings.Repeat("a", 12<<20)
	assistant := map[string]any{"message": map[string]any{"content": []map[string]string{{"type": "text", "text": large}}}}
	a := ImportArchive{}
	a.body = append(a.body, importTestEvent(0, "assistant/message", assistant, "append")...)
	a.body = append(a.body, importTestEvent(1, "user/message", importTestUser("keep this user", "user"), "append")...)
	a.body = append(a.body, importTestEvent(2, "assistant/message", assistant, "append")...)
	a.body = append(a.body, importTestEvent(3, "assistant/message", assistant, "append")...)
	if err := a.project(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !a.Shortened || len(a.Messages) != 3 || a.Messages[0].Role != "user" || a.Messages[0].Text != "keep this user" || a.Messages[1].Text != large || a.Messages[2].Text != large {
		t.Fatal("shortened history lost opening user or reordered retained messages")
	}
}

func TestImportAdmissionRejectsBlankOnlyUserText(t *testing.T) {
	for _, text := range []string{"   ", "\t\n", " nonblank "} {
		root, path, _ := importFixture(t, false)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		header, _, _ := bytes.Cut(b, []byte("\n"))
		b = append(append(header, '\n'), importTestEvent(0, "user/message", importTestUser(text, "user"), "append")...)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = ReadImportArchive(context.Background(), root, path)
		if (err != nil) != (strings.TrimSpace(text) == "") {
			t.Fatalf("source admission for %q: %v", text, err)
		}
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
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint, Env: map[string]string{credentialsPathEnv: ""}, Chat: true}
	ref, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	preparedArchive, err := ReadImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath)
	if err != nil || preparedArchive.ID != a.ID || preparedArchive.CWD == cwd || !bytes.Equal(preparedArchive.body, a.body) {
		t.Fatalf("private native history=%+v,%v", preparedArchive, err)
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
	preparedArchive, err = ReadImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath)
	if err != nil || len(preparedArchive.Messages) != 3 || preparedArchive.Messages[2].Text != "later AO turn" {
		t.Fatalf("lost later turn: %+v,%v", preparedArchive, err)
	}
	if err := p.PrepareRuntimeLaunch(ctx, ports.WorkspaceHookConfig{ImportConfigDir: ref.ConfigDir, NativeSessionID: ref.NativeSessionID, WorkspacePath: r.LaunchCWD, Env: map[string]string{"DSH_HOME": ref.ConfigDir}}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(r.LaunchCWD, alias); err != nil {
		t.Skipf("workspace symlink unavailable: %v", err)
	}
	visited := 0
	if err := VisitPrivateHistory(ctx, ref.ConfigDir, ref.NativeSessionID, alias, func(NativeHistoryEvent) error { visited++; return nil }); err != nil {
		t.Fatal(err)
	}
	if visited != 5 {
		t.Fatalf("alias replay visited %d events", visited)
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
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint, Env: map[string]string{credentialsPathEnv: ""}}
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

func TestImportRequiresExplicitHeaderAndEventCoordinates(t *testing.T) {
	for _, mutation := range []string{"seq missing", "seq null", "createdAt null", "isSeeded null", "delegationDepth null", "isSeeded wrong type", "startSeq missing", "startSeq null", "endSeq missing", "endSeq null"} {
		t.Run(mutation, func(t *testing.T) {
			root, path, _ := importFixture(t, false)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			header, body, _ := bytes.Cut(b, []byte("\n"))
			if strings.HasPrefix(mutation, "seq ") {
				first, rest, _ := bytes.Cut(body, []byte("\n"))
				var row map[string]any
				if err := json.Unmarshal(first, &row); err != nil {
					t.Fatal(err)
				}
				if mutation == "seq missing" {
					delete(row, "seq")
				} else {
					row["seq"] = nil
				}
				first, err = json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				body = append(append(first, '\n'), rest...)
			} else if strings.HasPrefix(mutation, "startSeq") || strings.HasPrefix(mutation, "endSeq") {
				op := map[string]any{"op": "replace", "startSeq": 0, "endSeq": 0}
				field := strings.Split(mutation, " ")[0]
				if strings.HasSuffix(mutation, "missing") {
					delete(op, field)
				} else {
					op[field] = nil
				}
				body = append(body, importTestEvent(4, "user/message", importTestUser("replacement", "user"), op)...)
			} else {
				var h map[string]any
				if err := json.Unmarshal(header, &h); err != nil {
					t.Fatal(err)
				}
				field := strings.Split(mutation, " ")[0]
				h[field] = nil
				if strings.HasSuffix(mutation, "wrong type") {
					h[field] = "false"
				}
				header, err = json.Marshal(h)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, append(append(header, '\n'), body...), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadImportArchive(context.Background(), root, path); err == nil {
				t.Fatal("accepted incomplete native coordinates")
			}
		})
	}
}

func TestPrivateImportWithSymlinkedDataAncestor(t *testing.T) {
	root, path, cwd := importFixture(t, false)
	a, err := ReadImportArchive(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	physical := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skip(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: filepath.Join(alias, "data"), SourceFingerprint: a.Fingerprint, Env: map[string]string{credentialsPathEnv: ""}}
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadImportArchive(context.Background(), ref.ConfigDir, ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	// Descendant symlinks remain forbidden even when ancestors are aliases.
	original, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadImportArchive(context.Background(), ref.ConfigDir, ref.TranscriptPath); err == nil {
		t.Fatal("accepted descendant symlink")
	}
}

func TestCredentialChangeCannotReplaceEvolvedPrivateHistory(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t, false)
	a, err := ReadImportArchive(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint, Env: map[string]string{credentialsPathEnv: ""}}
	ref, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(ref.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(importTestEvent(4, "user/message", importTestUser("private later turn", "user"), "append")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "credentials.yaml")
	if err := os.WriteFile(other, []byte("version: 1\nrefs: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.Env[credentialsPathEnv] = other
	if _, err := p.PrepareImportedSession(ctx, r); err == nil || !strings.Contains(err.Error(), "different credential store") {
		t.Fatalf("credential conflict=%v", err)
	}
	after, err := os.ReadFile(ref.TranscriptPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("private turn lost: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(ref.ConfigDir))
	if err != nil || len(entries) != 1 {
		t.Fatalf("created another native home: %v,%v", entries, err)
	}
}

func TestPrivateHistoryCanGrowBeyondSourceAdmissionBudget(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t, false)
	a, err := ReadImportArchive(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint, Env: map[string]string{credentialsPathEnv: ""}}
	ref, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(ref.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Valid JSON whitespace pads bounded rows; no giant message allocation is
	// needed to prove that total private growth no longer applies admission.
	padding := bytes.Repeat([]byte(" "), 12<<20)
	for i := 0; i < 22; i++ {
		row := importTestEvent(4+i, "user/message", importTestUser(fmt.Sprintf("later-%d", i), "user"), "append")
		if _, err := f.Write(row[:len(row)-1]); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(padding); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath); err == nil {
		t.Fatal("source admission budget was removed")
	}
	if got, err := p.ProbeNativeSession(ctx, ref); err != nil || got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("grown history probe=%v,%v", got, err)
	}
	if _, ok, err := p.LocateTranscript(ctx, ref); err != nil || !ok {
		t.Fatalf("grown history locate=%v,%v", ok, err)
	}
	if err := p.PrepareRuntimeLaunch(ctx, ports.WorkspaceHookConfig{ImportConfigDir: ref.ConfigDir, NativeSessionID: ref.NativeSessionID, WorkspacePath: r.LaunchCWD, Env: map[string]string{"DSH_HOME": ref.ConfigDir}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateImportedACP(ctx, ref.ConfigDir, ref.NativeSessionID, r.LaunchCWD); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedDeepSeekReopenAndRetrySurviveCLIUpgrade(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t, false)
	a, err := ReadImportArchive(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plugin{resolvedBinary: "fake-dsh", importVersion: func(context.Context) (string, error) { return supportedImportVersion, nil }}
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint, Env: map[string]string{credentialsPathEnv: ""}}
	ref, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	p.importVersion = func(context.Context) (string, error) { return "0.3.0", nil }
	if retry, err := p.PrepareImportedSession(ctx, r); err != nil || retry != ref {
		t.Fatalf("prepared retry after update=%+v,%v", retry, err)
	}
	cfg := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: r.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: ref.NativeSessionID, ports.MetadataKeyImportSourceNativeID: importTestID, ports.MetadataKeyImportConfigDir: ref.ConfigDir}}, Prompt: "continue"}
	if _, ok, err := p.GetRestoreCommand(ctx, cfg); err != nil || !ok {
		t.Fatalf("restore after update=%v,%v", ok, err)
	}
	if err := p.PrepareRuntimeLaunch(ctx, ports.WorkspaceHookConfig{ImportConfigDir: ref.ConfigDir, NativeSessionID: ref.NativeSessionID, WorkspacePath: r.LaunchCWD, Env: map[string]string{"DSH_HOME": ref.ConfigDir}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateImportedACP(ctx, ref.ConfigDir, ref.NativeSessionID, r.LaunchCWD); err != nil {
		t.Fatal(err)
	}
	r.DataDir = t.TempDir()
	if _, err := p.PrepareImportedSession(ctx, r); err == nil {
		t.Fatal("new preparation accepted unknown CLI")
	}
}
