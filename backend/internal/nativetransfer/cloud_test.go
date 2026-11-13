package nativetransfer

import (
	"bytes"
	"context"
	"errors"
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
