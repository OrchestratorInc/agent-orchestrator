package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandCodeV3ActiveBranch(t *testing.T) {
	root := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	path := filepath.Join(root, "projects", "work-repo", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	text := `{"type":"session","version":3,"id":"` + id + `","cwd":"/work/repo","timestamp":"2026-11-03T00:00:00Z"}` + "\n" +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-11-03T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"repair widget"}]}}` + "\n" +
		`{"type":"message","id":"old","parentId":"u1","timestamp":"2026-11-03T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"abandoned"}]}}` + "\n" +
		`{"type":"message","id":"tool","parentId":"u1","timestamp":"2026-11-03T00:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":"tool noise"}]}}` + "\n" +
		`{"type":"message","id":"a1","parentId":"tool","timestamp":"2026-11-03T00:00:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"repaired"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := discoverCommandCode(context.Background(), root)
	if err != nil || len(files) != 1 || files[0].nativeID() != id {
		t.Fatalf("discovery=%+v err=%v", files, err)
	}
	got, err := readCommandCodeTranscript(context.Background(), root, path)
	if err != nil || got.cwd != "/work/repo" || got.title != "repair widget" || len(got.messages) != 2 || got.messages[1].Text != "repaired" {
		t.Fatalf("transcript=%+v err=%v", got, err)
	}
	// The native v3 loader discards entries without required timestamps.
	invalid := strings.ReplaceAll(text, `"parentId":null,"timestamp":"2026-11-03T00:00:00Z"`, `"parentId":null`)
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCommandCodeTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted history the native loader would discard")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(text, `"version":3`, `"version":2`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCommandCodeTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted unsupported native format")
	}
}

func TestCommandCodeDiscoveryRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "projects", "repo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "11111111-2222-4333-8444-555555555555.jsonl")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	files, err := discoverCommandCode(context.Background(), root)
	if err != nil || len(files) != 0 {
		t.Fatalf("symlink discovery=%+v err=%v", files, err)
	}
	if _, err := readCommandCodeTranscript(context.Background(), root, path); err == nil {
		t.Fatal("read outside provider root")
	}
}
