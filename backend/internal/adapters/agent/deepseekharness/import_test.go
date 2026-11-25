package deepseekharness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	return importFixtureWithID(t, compressed, importTestID)
}

func importFixtureWithID(t *testing.T, compressed bool, id string) (root, path, cwd string) {
	t.Helper()
	root, cwd = t.TempDir(), t.TempDir()
	var err error
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(map[string]any{"type": "session", "version": 4, "id": id, "cwd": cwd, "createdAt": 1790000000000, "isSeeded": false, "delegationDepth": 0})
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
	path = filepath.Join(root, "sessions", importProjectKey(cwd), id, name)
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

func TestImportValidatesSurfaceReplacementsAndPreservesAppendTranscript(t *testing.T) {
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
	if err != nil || len(a.Messages) != 2 || a.Messages[0].Text != "opening marker" || a.Messages[1].Text != "remembered" {
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
	a.body = append(a.body, importTestEvent(4, "user/message", importTestUser("compacted model context", "runtime-context"), map[string]any{"op": "replace", "startSeq": 0, "endSeq": 3})...)
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

func TestHeadlessDefaultIDSurvivesPrivatePreparationAndReopen(t *testing.T) {
	ctx := context.Background()
	id := "session-" + importTestID
	root, path, cwd := importFixtureWithID(t, true, id)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := ReadImportArchive(ctx, root, path)
	if err != nil || archive.ID != id {
		t.Fatalf("native default ID rejected or rewritten: %+v,%v", archive, err)
	}
	p := &Plugin{resolvedBinary: "fake-dsh", importVersion: func(context.Context) (string, error) { return supportedImportVersion, nil }}
	request := ports.ImportedSessionRelocation{NativeSessionID: id, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: archive.Fingerprint, Env: map[string]string{credentialsPathEnv: ""}, Chat: true}
	ref, err := p.PrepareImportedSession(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if ref.NativeSessionID != id || filepath.Base(filepath.Dir(ref.TranscriptPath)) != id {
		t.Fatalf("private ID changed: %+v", ref)
	}
	if err := ValidateImportedACP(ctx, ref.ConfigDir, id, request.LaunchCWD); err != nil {
		t.Fatal(err)
	}
	if err := p.PrepareRuntimeLaunch(ctx, ports.WorkspaceHookConfig{NativeSessionID: id, ImportConfigDir: ref.ConfigDir, WorkspacePath: request.LaunchCWD, Env: map[string]string{"DSH_HOME": ref.ConfigDir}}); err != nil {
		t.Fatal(err)
	}
	cfg := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: request.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id, ports.MetadataKeyImportSourceNativeID: id, ports.MetadataKeyImportConfigDir: ref.ConfigDir}}, Prompt: "continue"}
	argv, ok, err := p.GetRestoreCommand(ctx, cfg)
	if err != nil || !ok || !slices.Contains(argv, id) {
		t.Fatalf("prefixed native restore=%v,%v,%v", argv, ok, err)
	}
	events := 0
	if err := VisitPrivateHistory(ctx, ref.ConfigDir, id, request.LaunchCWD, func(NativeHistoryEvent) error { events++; return nil }); err != nil || events != 4 {
		t.Fatalf("prefixed native replay=%d,%v", events, err)
	}
	if retry, err := p.PrepareImportedSession(ctx, request); err != nil || retry != ref {
		t.Fatalf("retry changed native identity: %+v,%v", retry, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("source changed: %v", err)
	}
}

func TestImportNativeIdentityAllowsOnlyKnownSpellings(t *testing.T) {
	for _, id := range []string{importTestID, "session-" + importTestID} {
		if !IsImportSessionID(id) {
			t.Fatalf("rejected native identity %q", id)
		}
	}
	for _, id := range []string{"session-", "session-not-a-uuid", "session-session-" + importTestID, "prefix-" + importTestID, "session-" + importTestID + "-extra", "../session-" + importTestID} {
		if IsImportSessionID(id) {
			t.Fatalf("accepted unsupported identity %q", id)
		}
	}
	root, path, _ := importFixtureWithID(t, false, "session-"+importTestID)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte(`"id":"session-`+importTestID+`"`), []byte(`"id":"`+importTestID+`"`), 1)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadImportArchive(context.Background(), root, path); err == nil {
		t.Fatal("accepted directory/header identity mismatch")
	}
}

func TestImportAdmissionPreservesHumanTranscriptWhenSourceEndsAfterCompaction(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		for _, later := range []bool{false, true} {
			t.Run(fmt.Sprintf("compressed=%v/later=%v", compressed, later), func(t *testing.T) {
				root, path, _ := importFixtureWithID(t, false, "session-"+importTestID)
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				header, _, _ := bytes.Cut(original, []byte("\n"))
				body := importTestEvent(0, "user/message", importTestUser("keep original user marker", "user"), "append")
				body = append(body, importTestEvent(1, "assistant/message", map[string]any{"message": map[string]any{"id": "assistant1", "content": []map[string]string{{"type": "text", "text": "remembered original marker"}}}}, "append")...)
				body = append(body, importTestEvent(2, "user/message", importTestUser("summary model context", "runtime-context"), map[string]any{"op": "replace", "startSeq": 0, "endSeq": 1})...)
				if later {
					body = append(body, importTestEvent(3, "user/message", importTestUser("later private turn", "user"), "append")...)
				}
				data := append(append(header, '\n'), body...)
				if compressed {
					enc, err := zstd.NewWriter(nil, zstd.WithEncoderCRC(true))
					if err != nil {
						t.Fatal(err)
					}
					data = enc.EncodeAll(data, nil)
					enc.Close()
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					path += ".zstd"
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				archive, err := ReadImportArchive(context.Background(), root, path)
				want := 2
				if later {
					want++
				}
				if err != nil || len(archive.Messages) != want {
					t.Fatalf("compacted source lost human history: %+v,%v", archive, err)
				}
				if archive.Messages[0].Text != "keep original user marker" || archive.Messages[1].Text != "remembered original marker" {
					t.Fatalf("compaction changed human transcript: %+v", archive.Messages)
				}
				if later && archive.Messages[2].Text != "later private turn" {
					t.Fatal("later turn displaced earlier human history")
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, after) {
					t.Fatalf("source changed: %v", err)
				}
			})
		}
	}
}

// A parallel scan of several compressed journals must queue on one shared
// byte budget instead of holding every per-file worst case at once.
func TestConcurrentCompressedReadsShareOneByteBudget(t *testing.T) {
	type candidate struct{ root, path string }
	var candidates []candidate
	var largest int64
	for i := range 8 {
		root, path, _ := importFixtureWithID(t, true, fmt.Sprintf("00000000-0000-4000-8000-%012d", 600+i))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		largest = max(largest, info.Size())
		candidates = append(candidates, candidate{root, path})
	}
	// Room for exactly one worst-case compressed read.
	one := largest + maxImportVisible + maxImportDecoded
	previous := importReads
	importReads = newImportReadBudget(one)
	t.Cleanup(func() { importReads = previous })
	errs := make(chan error, len(candidates))
	for _, c := range candidates {
		go func() {
			a, err := ReadImportArchive(context.Background(), c.root, c.path)
			if err == nil && len(a.Messages) != 2 {
				err = fmt.Errorf("archive=%+v", a)
			}
			errs <- err
		}()
	}
	for range candidates {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if peak := importReads.peak.Load(); peak <= 0 || peak > one {
		t.Fatalf("concurrent reads held %d bytes; budget %d", peak, one)
	}
	if held := importReads.held.Load(); held != 0 {
		t.Fatalf("read budget leaked %d bytes", held)
	}
	ctx, cancel := context.WithCancel(context.Background())
	release, err := importReads.acquire(ctx, largest, true)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := ReadImportArchive(ctx, candidates[0].root, candidates[0].path); err == nil {
		t.Fatal("cancelled read bypassed an exhausted budget")
	}
	release()
}

// A root prepared under an older pin must stay openable after the pin moves:
// reopen requires only a recorded, root-bound proof version.
func TestPreparedRootWithOlderProofVersionStillValidates(t *testing.T) {
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
	// Re-home the prepared root as if an older AO release had written it.
	rehome := func(version string) string {
		t.Helper()
		proof, err := readImportProof(ref.ConfigDir)
		if err != nil {
			t.Fatal(err)
		}
		proof.Version = version
		encoded, name, err := importProofBytes(proof)
		if err != nil {
			t.Fatal(err)
		}
		older := filepath.Join(filepath.Dir(ref.ConfigDir), name)
		if err := os.Rename(ref.ConfigDir, older); err != nil {
			t.Fatal(err)
		}
		ref.ConfigDir = older
		if err := os.WriteFile(filepath.Join(older, importProofName), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, profile := range []string{"acp", headlessProfile} {
			patch, err := privateImportPatch(older, proof.CredentialsPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(older, "profiles", profile, "cordis.patch.yml"), patch, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return older
	}
	older := rehome("0.1.0-older-pin")
	if got, err := validatePrivateImport(ctx, older, importTestID, r.LaunchCWD); err != nil || got == "" {
		t.Fatalf("older proof version reopen = %q, %v", got, err)
	}
	if err := ValidateImportedACP(ctx, older, importTestID, r.LaunchCWD); err != nil {
		t.Fatalf("older proof version ACP reopen: %v", err)
	}
	empty := rehome("")
	if _, err := validatePrivateImport(ctx, empty, importTestID, r.LaunchCWD); err == nil {
		t.Fatal("reopen accepted a proof without a recorded version")
	}
}

// The private DSH_HOME carries only AO's patch, so an import of a chat whose
// own profile patch sets a custom endpoint would silently use dsh's default
// one. Such an import is refused with a message the user can act on.
func TestImportRefusesProfileCustomEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, path, patch string
		refused           bool
	}{
		{"profile baseURL", filepath.Join("profiles", "acp", "cordis.patch.yml"), "- id: llm-openai-compatible\n  config:\n    baseURL: https://llm.example.test/v1\n", true},
		{"root base_url", "cordis.patch.yml", "providers:\n  custom:\n    base_url: https://llm.example.test/v1\n", true},
		{"unparseable patch", "cordis.patch.yml", "- id: [unterminated\n", true},
		{"patch without endpoint", filepath.Join("profiles", "acp", "cordis.patch.yml"), "- id: llm-deepseek\n  config:\n    model: deepseek-chat\n", false},
		{"empty endpoint", "cordis.patch.yml", "- id: llm-deepseek\n  config:\n    baseURL: \"\"\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, path, cwd := importFixture(t, false)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, tc.path)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tc.path), []byte(tc.patch), 0o600); err != nil {
				t.Fatal(err)
			}
			a, err := ReadImportArchive(context.Background(), root, path)
			if err != nil {
				t.Fatal(err)
			}
			p := New()
			p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
			r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint, Env: map[string]string{credentialsPathEnv: ""}}
			_, err = p.PrepareImportedSession(context.Background(), r)
			if !tc.refused {
				if err != nil {
					t.Fatalf("patch without a custom endpoint refused: %v", err)
				}
				return
			}
			var refused *ports.ImportRefusedError
			if !errors.As(err, &refused) || strings.Contains(err.Error(), "example.test") {
				t.Fatalf("custom endpoint import error = %v, want a user-facing refusal without the endpoint", err)
			}
			if entries, _ := os.ReadDir(filepath.Join(r.DataDir, "native-imports", adapterID)); len(entries) != 0 {
				t.Fatalf("refused import left private state: %v", entries)
			}
		})
	}
}
