package nativetransfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteClaudeTranscriptForCloudUsesPlaceholders(t *testing.T) {
	f := newClaudeFixture(t)
	var out bytes.Buffer
	id, err := WriteClaudeTranscriptForCloud(context.Background(), &out, ClaudeExportOptions{ConfigDir: f.cfgA, NativeID: strings.ToUpper(testID), Cwd: f.cwdA}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if id != testID {
		t.Fatalf("id = %q, want %q", id, testID)
	}
	got := out.String()
	for _, want := range []string{
		`"cwd":"` + CloudCwdPlaceholder + `"`,
		`"file_path":"` + CloudCwdPlaceholder + `/notes.txt"`,
		`Output saved to ` + CloudProjectDirPlaceholder + `/` + testID + `/tool-results/x.txt`,
		// A signed thinking block and paths that only share a prefix stay as they were.
		`"thinking":"the user works in ` + f.cwdA + `"`,
		`see ` + f.cwdA + `0/x and /prefix` + f.cwdA + `/y`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript lacks %s", want)
		}
	}
	if strings.Contains(got, `"cwd":"`+f.cwdA) || strings.Contains(got, f.projA) {
		t.Errorf("transcript still names this machine's paths:\n%s", got)
	}
	if lines := strings.Count(got, "\n"); lines != len(f.lines) {
		t.Errorf("transcript has %d lines, want %d", lines, len(f.lines))
	}
}

func TestWriteClaudeTranscriptForCloudRefusesOversize(t *testing.T) {
	f := newClaudeFixture(t)
	path, _, size, err := ClaudeTranscript(context.Background(), ClaudeExportOptions{ConfigDir: f.cfgA, NativeID: testID, Cwd: f.cwdA})
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Size() != size || filepath.Base(path) != testID+".jsonl" {
		t.Fatalf("ClaudeTranscript = %s, %d bytes", path, size)
	}
	_, err = WriteClaudeTranscriptForCloud(context.Background(), &bytes.Buffer{}, ClaudeExportOptions{ConfigDir: f.cfgA, NativeID: testID, Cwd: f.cwdA}, size-1)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestWriteClaudeArchiveFromCloudPlacesTheConversationHere(t *testing.T) {
	// cfgA and cwdA stand for the sandbox; the transcript arrives as one file.
	f := newClaudeFixture(t)
	inbox := filepath.Join(t.TempDir(), testID+".jsonl")
	data, err := os.ReadFile(filepath.Join(f.projA, testID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, inbox, string(data))
	var archive bytes.Buffer
	if _, err := WriteClaudeArchiveFromCloud(context.Background(), &archive, inbox, f.projA); err != nil {
		t.Fatal(err)
	}
	res, err := ImportClaude(context.Background(), &archive, ClaudeImportOptions{ConfigDir: f.cfgB, LaunchCwd: f.cwdB})
	if err != nil {
		t.Fatal(err)
	}
	nameB, _ := ClaudeProjectDirName(f.cwdB)
	projB := filepath.Join(f.cfgB, "projects", nameB)
	if res.NativeID != testID || res.TranscriptPath != filepath.Join(projB, testID+".jsonl") {
		t.Fatalf("import = %+v", res)
	}
	placed, _ := os.ReadFile(res.TranscriptPath)
	got := string(placed)
	for _, want := range []string{
		`"cwd":"` + f.cwdB + `"`,
		`Output saved to ` + projB + `/` + testID + `/tool-results/x.txt`,
		`"thinking":"the user works in ` + f.cwdA + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("placed transcript lacks %s", want)
		}
	}
	if strings.Contains(got, `"cwd":"`+f.cwdA) || strings.Contains(got, f.projA) {
		t.Errorf("placed transcript still names the sandbox's paths:\n%s", got)
	}
}

func sidecarEntries(t *testing.T, gz []byte) map[string]string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		out[hdr.Name] = string(data)
	}
}

func TestClaudeSidecarGoesToCloudWithPlaceholders(t *testing.T) {
	f := newClaudeFixture(t)
	opts := ClaudeExportOptions{ConfigDir: f.cfgA, NativeID: testID, Cwd: f.cwdA}
	info, err := ClaudeSidecarInfo(context.Background(), opts)
	if err != nil || info.Subagents != 1 || info.Bytes == 0 {
		t.Fatalf("ClaudeSidecarInfo = %+v, %v", info, err)
	}
	var out bytes.Buffer
	ok, err := WriteClaudeSidecarForCloud(context.Background(), &out, opts, 1<<20)
	if err != nil || !ok {
		t.Fatalf("WriteClaudeSidecarForCloud = %v, %v", ok, err)
	}
	got := sidecarEntries(t, out.Bytes())
	if !strings.Contains(got["subagents/agent-1.jsonl"], `"cwd":"`+CloudCwdPlaceholder+`"`) || strings.Contains(got["subagents/agent-1.jsonl"], f.cwdA) {
		t.Errorf("subagent transcript = %q", got["subagents/agent-1.jsonl"])
	}
	if got["tool-results/x.txt"] != "large tool output\n" || len(got) != 2 {
		t.Errorf("sidecar entries = %v", got)
	}
	if _, err := WriteClaudeSidecarForCloud(context.Background(), &bytes.Buffer{}, opts, 10); !errors.Is(err, ErrTooLarge) {
		t.Errorf("small cap err = %v, want ErrTooLarge", err)
	}
}

func TestClaudeSidecarFromCloudIsPlacedHere(t *testing.T) {
	// cfgA and cwdA stand for the sandbox, which sends its folder as it is.
	f := newClaudeFixture(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	tw := tar.NewWriter(zw)
	for _, rel := range []string{"subagents/agent-1.jsonl", "tool-results/x.txt"} {
		data, err := os.ReadFile(filepath.Join(f.projA, testID, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		_ = tw.WriteHeader(&tar.Header{Name: rel, Typeflag: tar.TypeReg, Size: int64(len(data)), Mode: 0o600})
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = zw.Close()
	inbox := t.TempDir()
	data, _ := os.ReadFile(filepath.Join(f.projA, testID+".jsonl"))
	writeFile(t, filepath.Join(inbox, testID+".jsonl"), string(data))
	if err := ExtractClaudeSidecar(bytes.NewReader(gz.Bytes()), filepath.Join(inbox, testID), 1<<20); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err := WriteClaudeArchiveFromCloud(context.Background(), &archive, filepath.Join(inbox, testID+".jsonl"), f.projA); err != nil {
		t.Fatal(err)
	}
	res, err := ImportClaude(context.Background(), &archive, ClaudeImportOptions{ConfigDir: f.cfgB, LaunchCwd: f.cwdB})
	if err != nil {
		t.Fatal(err)
	}
	projB := filepath.Dir(res.TranscriptPath)
	sub, err := os.ReadFile(filepath.Join(projB, testID, "subagents", "agent-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sub), `"cwd":"`+f.cwdB+`"`) {
		t.Errorf("placed subagent transcript = %q", sub)
	}
	if tool, _ := os.ReadFile(filepath.Join(projB, testID, "tool-results", "x.txt")); string(tool) != "large tool output\n" {
		t.Errorf("placed tool output = %q", tool)
	}
}

func TestExtractClaudeSidecarRefusesUnsafeEntries(t *testing.T) {
	for _, hdr := range []tar.Header{
		{Name: "../escape.txt", Typeflag: tar.TypeReg, Size: 1, Mode: 0o600},
		{Name: "/abs.txt", Typeflag: tar.TypeReg, Size: 1, Mode: 0o600},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o600},
	} {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		h := hdr
		_ = tw.WriteHeader(&h)
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("x"))
		}
		_ = tw.Close()
		_ = zw.Close()
		if err := ExtractClaudeSidecar(&buf, t.TempDir(), 1<<20); err == nil {
			t.Errorf("entry %q accepted", hdr.Name)
		}
	}
}
