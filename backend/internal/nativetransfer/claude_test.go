package nativetransfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testID = "7c1e2f4a-0b6d-4e1f-9a3b-5c8d7e6f00a2"

type claudeFixture struct {
	cfgA, cwdA, projA string
	cfgB, cwdB        string
	lines             []string
}

func newClaudeFixture(t *testing.T) claudeFixture {
	t.Helper()
	root := t.TempDir()
	f := claudeFixture{cfgA: filepath.Join(root, "alice", ".claude"), cfgB: filepath.Join(root, "bob", ".claude")}
	f.cwdA = mkdirReal(t, filepath.Join(root, "alice", "worktrees", "web", "web-3"))
	f.cwdB = mkdirReal(t, filepath.Join(root, "bob", "worktrees", "web", "web-9"))
	name, err := ClaudeProjectDirName(f.cwdA)
	if err != nil {
		t.Fatal(err)
	}
	f.projA = filepath.Join(f.cfgA, "projects", name)
	f.lines = []string{
		`{"type":"user","cwd":"` + f.cwdA + `","sessionId":"` + testID + `","version":"2.1.300","message":{"role":"user","content":"remember PELICAN-42"}}`,
		`{"type":"assistant","cwd":"` + f.cwdA + `","version":"2.1.307","message":{"role":"assistant","content":[{"type":"thinking","thinking":"the user works in ` + f.cwdA + `","signature":"c2ln"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"` + f.cwdA + `/notes.txt"}}]},"costUSD":1.50}`,
		`{"type":"user","cwd":"` + f.cwdA + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"Output saved to ` + f.projA + `/` + testID + `/tool-results/x.txt"}]}}`,
		`{"type":"summary","summary":"no paths in this record"}`,
		`{"type":"user","cwd":"` + f.cwdA + `","message":{"role":"user","content":"see ` + f.cwdA + `0/x and /prefix` + f.cwdA + `/y"}}`,
	}
	writeFile(t, filepath.Join(f.projA, testID+".jsonl"), strings.Join(f.lines, "\n")+"\n")
	writeFile(t, filepath.Join(f.projA, testID, "subagents", "agent-1.jsonl"), `{"type":"user","cwd":"`+f.cwdA+`","message":{"role":"user","content":"sub"}}`+"\n")
	writeFile(t, filepath.Join(f.projA, testID, "tool-results", "x.txt"), "large tool output\n")
	return f
}

func mkdirReal(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func exportFixture(t *testing.T, f claudeFixture) (*bytes.Buffer, Manifest) {
	t.Helper()
	var buf bytes.Buffer
	m, err := ExportClaude(context.Background(), &buf, ClaudeExportOptions{ConfigDir: f.cfgA, NativeID: strings.ToUpper(testID)})
	if err != nil {
		t.Fatal(err)
	}
	return &buf, m
}

func TestExportImportClaudeMovesConversation(t *testing.T) {
	f := newClaudeFixture(t)
	before, err := os.ReadFile(filepath.Join(f.projA, testID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	buf, m := exportFixture(t, f)
	if m.SourceCwd != f.cwdA || m.SourceProjectDir != f.projA || m.CLIVersion != "2.1.307" || len(m.Files) != 3 {
		t.Fatalf("manifest = %+v", m)
	}

	res, err := ImportClaude(context.Background(), buf, ClaudeImportOptions{ConfigDir: f.cfgB, LaunchCwd: f.cwdB})
	if err != nil {
		t.Fatal(err)
	}
	nameB, _ := ClaudeProjectDirName(f.cwdB)
	projB := filepath.Join(f.cfgB, "projects", nameB)
	if res.TranscriptPath != filepath.Join(projB, testID+".jsonl") || res.Existing || res.RewrittenLines != 5 {
		t.Fatalf("result = %+v", res)
	}

	got := readLines(t, res.TranscriptPath)
	var recs []map[string]any
	for _, line := range got {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, rec)
	}
	for _, i := range []int{0, 1, 2, 4} {
		if recs[i]["cwd"] != f.cwdB {
			t.Errorf("record %d cwd = %v, want %s", i, recs[i]["cwd"], f.cwdB)
		}
	}
	content := recs[1]["message"].(map[string]any)["content"].([]any)
	if thinking := content[0].(map[string]any)["thinking"]; thinking != "the user works in "+f.cwdA {
		t.Errorf("thinking block changed: %v", thinking)
	}
	if path := content[1].(map[string]any)["input"].(map[string]any)["file_path"]; path != f.cwdB+"/notes.txt" {
		t.Errorf("tool_use path = %v", path)
	}
	if !strings.Contains(got[1], `"costUSD":1.50`) {
		t.Errorf("number literal not preserved: %s", got[1])
	}
	if !strings.Contains(got[2], projB+"/"+testID+"/tool-results/x.txt") {
		t.Errorf("tool result path not rewritten: %s", got[2])
	}
	if got[3] != f.lines[3] {
		t.Errorf("record without paths changed:\n got %s\nwant %s", got[3], f.lines[3])
	}
	msg := recs[4]["message"].(map[string]any)["content"]
	if msg != "see "+f.cwdA+"0/x and /prefix"+f.cwdA+"/y" {
		t.Errorf("non-matching paths rewritten: %v", msg)
	}
	if sub := readLines(t, filepath.Join(projB, testID, "subagents", "agent-1.jsonl")); !strings.Contains(sub[0], `"cwd":"`+f.cwdB+`"`) {
		t.Errorf("subagent cwd not rewritten: %s", sub[0])
	}
	if out, _ := os.ReadFile(filepath.Join(projB, testID, "tool-results", "x.txt")); string(out) != "large tool output\n" {
		t.Errorf("tool output = %q", out)
	}
	if after, _ := os.ReadFile(filepath.Join(f.projA, testID+".jsonl")); !bytes.Equal(after, before) {
		t.Error("source transcript changed")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(projB, ".nativetransfer-*")); len(leftovers) != 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}

func TestImportClaudeKeepsExistingTranscript(t *testing.T) {
	f := newClaudeFixture(t)
	buf, _ := exportFixture(t, f)
	archive := buf.Bytes()
	res, err := ImportClaude(context.Background(), bytes.NewReader(archive), ClaudeImportOptions{ConfigDir: f.cfgB, LaunchCwd: f.cwdB})
	if err != nil {
		t.Fatal(err)
	}
	// After a resume on B the copy holds turns the archive lacks.
	writeFile(t, res.TranscriptPath, "later turns\n")
	again, err := ImportClaude(context.Background(), bytes.NewReader(archive), ClaudeImportOptions{ConfigDir: f.cfgB, LaunchCwd: f.cwdB})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Existing {
		t.Fatalf("second import = %+v, want Existing", again)
	}
	if data, _ := os.ReadFile(res.TranscriptPath); string(data) != "later turns\n" {
		t.Fatalf("existing transcript overwritten: %q", data)
	}
}

func TestImportClaudeRejectsTamperedArchive(t *testing.T) {
	f := newClaudeFixture(t)
	_, m := exportFixture(t, f)
	files := map[string]string{}
	for _, file := range m.Files {
		data, err := os.ReadFile(filepath.Join(f.projA, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		files[file.Path] = string(data)
	}
	files[testID+".jsonl"] = strings.Replace(files[testID+".jsonl"], "PELICAN", "PELICAX", 1)

	_, err := ImportClaude(context.Background(), rawArchive(t, m, files), ClaudeImportOptions{ConfigDir: f.cfgB, LaunchCwd: f.cwdB})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	nameB, _ := ClaudeProjectDirName(f.cwdB)
	entries, _ := os.ReadDir(filepath.Join(f.cfgB, "projects", nameB))
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("left %s behind", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(f.cfgB, "projects", nameB, testID+".jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("transcript placed despite bad checksum: %v", err)
	}
}

func TestImportClaudeRejectsUnsafePaths(t *testing.T) {
	f := newClaudeFixture(t)
	for _, bad := range []string{"../escape.jsonl", testID + "/../x", "/abs.jsonl", "other-session.jsonl"} {
		m := Manifest{Version: formatVersion, Harness: HarnessClaudeCode, NativeID: testID, SourceCwd: f.cwdA, SourceProjectDir: f.projA,
			Files: []File{{Path: bad, Size: 1, SHA256: strings.Repeat("0", 64)}}}
		if _, err := ImportClaude(context.Background(), rawArchive(t, m, map[string]string{bad: "x"}), ClaudeImportOptions{ConfigDir: f.cfgB, LaunchCwd: f.cwdB}); err == nil {
			t.Errorf("path %q accepted", bad)
		}
	}
}

func TestWriteArchiveDetectsChangedSource(t *testing.T) {
	f := newClaudeFixture(t)
	_, m := exportFixture(t, f)
	writeFile(t, filepath.Join(f.projA, testID+".jsonl"), strings.Join(f.lines, "\n")+"\n{\"type\":\"user\"}\n")
	if err := writeArchive(context.Background(), &bytes.Buffer{}, m, f.projA); !errors.Is(err, ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
}

func TestClaudeProjectDirName(t *testing.T) {
	dir := mkdirReal(t, filepath.Join(t.TempDir(), "a.b", "c d_e"))
	got, err := ClaudeProjectDirName(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("/", "-", ".", "-", " ", "-", "_", "-").Replace(dir)
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	long := mkdirReal(t, filepath.Join(t.TempDir(), strings.Repeat("x", 210)))
	if _, err := ClaudeProjectDirName(long); !errors.Is(err, ErrUnsupportedPath) {
		t.Fatalf("long path err = %v", err)
	}
}

// rawArchive builds an archive without the checks writeArchive applies.
func rawArchive(t *testing.T, m Manifest, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	data, _ := json.Marshal(m)
	add := func(name string, body []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add(manifestName, data)
	for _, file := range m.Files {
		add(filesPrefix+file.Path, []byte(files[file.Path]))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExportClaudePrefersTheSessionFolder(t *testing.T) {
	f := newClaudeFixture(t)
	stale := filepath.Join(f.cfgA, "projects", "-a-stale-copy", testID+".jsonl")
	writeFile(t, stale, `{"type":"user","cwd":"/old","message":{"role":"user","content":"old"}}`+"\n")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(stale, later, later); err != nil {
		t.Fatal(err)
	}
	export := func(cwd string) Manifest {
		m, err := ExportClaude(context.Background(), &bytes.Buffer{}, ClaudeExportOptions{ConfigDir: f.cfgA, NativeID: testID, Cwd: cwd})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := export(f.cwdA).SourceProjectDir; got != f.projA {
		t.Fatalf("with cwd, exported %s, want the session folder %s", got, f.projA)
	}
	if got := export("").SourceProjectDir; got != filepath.Dir(stale) {
		t.Fatalf("without cwd, exported %s, want the newest copy %s", got, filepath.Dir(stale))
	}
}
