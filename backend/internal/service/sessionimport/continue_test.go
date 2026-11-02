package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const continueImportID = "11111111-2222-4333-8444-555555555555"

func continueImportFixture(t *testing.T, root string) string {
	t.Helper()
	return writeFile(t, filepath.Join(root, "sessions", continueImportID+".json"), `{"sessionId":"`+continueImportID+`","workspaceDirectory":"/work/original","history":[{"message":{"role":"system","content":"private instructions"}},{"message":{"role":"user","content":"make a greeting"}},{"message":{"role":"assistant","content":[{"type":"text","text":"Hello"},{"type":"tool_use","name":"bash","input":{"command":"private"}}]}},{"message":{"role":"tool","content":"private result"}}]}`)
}

func TestContinueDiscoversAndReadsExactNativeSessions(t *testing.T) {
	root := t.TempDir()
	path := continueImportFixture(t, root)
	writeFile(t, filepath.Join(root, "sessions", "sessions.json"), `[{"sessionId":"index-only"}]`)
	groups, err := discoverContinue(context.Background(), root)
	if err != nil || len(groups) != 1 || groups[0].key != continueImportID || groups[0].paths[0] != path {
		t.Fatalf("sessions = %+v, %v", groups, err)
	}
	got, err := readContinueTranscript(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: make a greeting", "assistant: Hello")
	if got.nativeID != continueImportID || got.cwd != "/work/original" || got.title != "make a greeting" {
		t.Fatalf("native session = %+v", got)
	}
}

func TestContinueRejectsMismatchedIdentityAndTornSession(t *testing.T) {
	root := t.TempDir()
	path := continueImportFixture(t, root)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{strings.Replace(string(data), continueImportID, "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", 1), `{"sessionId":`} {
		writeFile(t, path, body)
		if _, err := readContinueTranscript(context.Background(), root, path); err == nil {
			t.Fatal("accepted mismatched or torn native session")
		}
	}
}

func TestContinueImportConfinesSessionFiles(t *testing.T) {
	root := t.TempDir()
	outside := continueImportFixture(t, t.TempDir())
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, continueImportID+".json")
	if err := os.Symlink(outside, path); err != nil {
		t.Skip(err)
	}
	if _, err := readContinueTranscript(context.Background(), root, path); err == nil {
		t.Fatal("read escaping native session")
	}
	groups, err := discoverContinue(context.Background(), root)
	if err != nil || len(groups) != 0 {
		t.Fatalf("offered escaping native session: %+v, %v", groups, err)
	}
}
