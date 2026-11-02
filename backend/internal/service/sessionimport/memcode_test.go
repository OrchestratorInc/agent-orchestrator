package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const memcodeImportFixture = `{"session_id":"sess_01234567","saved_at":"2026-11-01T10:00:00Z","messages":[{"role":"system","content":[{"type":"text","text":"hidden instructions"}]},{"role":"user","content":[{"type":"text","text":"make a greeting"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"private thought"},{"type":"text","text":"Hello"},{"type":"tool_use","name":"bash","input":{"command":"private command"}}]},{"role":"user","content":[{"type":"tool_result","content":"private result"}]}]}`

func TestMemcodeDiscoversOnlyResumableProjectHistory(t *testing.T) {
	project := t.TempDir()
	path := writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), memcodeImportFixture)
	writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_89abcdef", "events.jsonl"), `{"kind":"user_message","text":"recall only"}`)
	groups, err := discoverMemcodeProject(context.Background(), project)
	if err != nil || len(groups) != 1 || groups[0].paths[0] != path || groups[0].key != "sess_01234567" {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	got, err := readMemcodeTranscript(context.Background(), project, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: make a greeting", "assistant: Hello")
	if got.nativeID != groups[0].key || got.cwd != canonical(project) || got.last.Format("15:04") != "10:00" {
		t.Fatalf("native metadata = %+v", got)
	}
}

func TestMemcodeImportRejectsMismatchedAndMalformedWireHistory(t *testing.T) {
	for _, body := range []string{
		strings.Replace(memcodeImportFixture, "sess_01234567", "sess_89abcdef", 1),
		`{"session_id":"sess_01234567","messages":[]}`,
		`{"session_id":"sess_01234567","messages":[{"role":"user","content":"not wire blocks"}]}`,
		`{"session_id":"sess_01234567","messages":[{"role":"invalid","content":[]}]}`,
	} {
		project := t.TempDir()
		path := writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), body)
		if _, err := readMemcodeTranscript(context.Background(), project, path); err == nil {
			t.Fatalf("accepted invalid native history: %s", body)
		}
	}
}

func TestMemcodeImportConfinesRepositoryHistory(t *testing.T) {
	project := t.TempDir()
	outside := writeFile(t, filepath.Join(t.TempDir(), "messages.json"), memcodeImportFixture)
	dir := filepath.Join(project, ".memcode", "sessions", "sess_01234567")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "messages.json")
	if err := os.Symlink(outside, path); err != nil {
		t.Skip(err)
	}
	if _, err := readMemcodeTranscript(context.Background(), project, path); err == nil {
		t.Fatal("read escaping transcript")
	}
	groups, err := discoverMemcodeProject(context.Background(), project)
	if err != nil || len(groups) != 0 {
		t.Fatalf("offered escaping transcript: %+v, %v", groups, err)
	}
}
