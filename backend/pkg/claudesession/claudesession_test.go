package claudesession

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectKey(t *testing.T) {
	if got := ProjectKey("/Users/a/.ao/w_1"); got != "-Users-a--ao-w-1" {
		t.Fatalf("key = %q", got)
	}
}

func TestResolveForkPointUsesLastLineInSourceFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "s.jsonl")
	// One API message is streamed as several lines; the cut must include all.
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"msg_A","content":[{"type":"thinking"}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"id":"msg_A","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"next"}}`,
		`{"type":"assistant","uuid":"b1","message":{"id":"msg_B","content":[{"type":"text","text":"later"}]}}`,
	}
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveForkPoint(context.Background(), file, "msg_A")
	if err != nil || got != "a2" {
		t.Fatalf("fork point = %q, %v; want a2", got, err)
	}
	if _, err := ResolveForkPoint(context.Background(), file, "msg_missing"); err == nil {
		t.Fatal("an unknown message must not fork the whole session")
	}
}

// The copied conversation names the original's worktree in tool inputs and
// results too, not only in cwd. Left as they are, the fork's agent edits the
// original's files, so every reference moves to the fork's worktree. A
// sibling folder that only shares the name's start is left alone.
func TestForkCopyPointsEveryWorktreePathAtTheFork(t *testing.T) {
	src, dst := "/w/fork-test/fork-test-1", "/w/fork-test/fork-test-4"
	record := map[string]any{
		"cwd": src,
		"message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "name": "Write", "input": map[string]any{"file_path": src + "/a.txt"}},
			map[string]any{"type": "tool_result", "content": "Wrote " + src + "/a.txt; left " + src + "0/b.txt alone"},
		}},
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	got := string(RewriteWorkspacePaths(line, []string{src}, dst))
	for _, want := range []string{`"cwd":"` + dst + `"`, `"file_path":"` + dst + `/a.txt"`, "Wrote " + dst + "/a.txt", src + "0/b.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten line lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ReplaceAll(got, src+"0/", ""), src) {
		t.Errorf("the original's worktree is still named:\n%s", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("rewritten line is not JSON: %s", got)
	}
}

func TestMoveSessionFileRewritesAndReplacesTheSource(t *testing.T) {
	dir := t.TempDir()
	src, dst := "/w/original", "/w/fork"
	from := filepath.Join(dir, "projects", ProjectKey(src), "child.jsonl")
	to := filepath.Join(dir, "projects", ProjectKey(dst), "child.jsonl")
	if err := os.MkdirAll(filepath.Dir(from), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := `{"cwd":"/w/original","uuid":"a"}` + "\n" + `{"cwd":"/w/original-2","uuid":"b"}` + "\n"
	if err := os.WriteFile(from, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MoveSessionFile(context.Background(), from, to, []string{src}, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"cwd":"/w/fork","uuid":"a"}` + "\n" + `{"cwd":"/w/original-2","uuid":"b"}` + "\n"
	if string(got) != want {
		t.Fatalf("moved file =\n%s\nwant\n%s", got, want)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("source copy kept: %v", err)
	}
}

// fakeNode is a stand-in for node that prints what the fork script would.
func fakeNode(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node")
	script := "#!/bin/sh\nprintf '%s\\n' '" + output + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunForkReturnsTheCopysSessionID(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	dir := t.TempDir()
	id, err := RunFork(context.Background(), fakeNode(t, `{"sessionId":"child-id"}`), "sdk.mjs", os.Environ(), "source-id", dir, "")
	if err != nil || id != "child-id" {
		t.Fatalf("RunFork = %q, %v; want child-id", id, err)
	}
	for _, bad := range []string{`{"sessionId":"source-id"}`, `{"sessionId":""}`, `{"sessionId":"../x"}`, `not json`} {
		if id, err := RunFork(context.Background(), fakeNode(t, bad), "sdk.mjs", os.Environ(), "source-id", dir, ""); err == nil {
			t.Errorf("RunFork with output %s = %q, want an error", bad, id)
		}
	}
}
