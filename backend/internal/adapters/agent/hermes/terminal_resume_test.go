package hermes

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// seedTerminalResumeHome builds a session dir whose native-home points at a
// Hermes home with a state.db holding the given session rows.
func seedTerminalResumeHome(t *testing.T, rows ...[3]string) (data, home string) {
	t.Helper()
	data, home = t.TempDir(), t.TempDir()
	dir := sessionDir(data, "session-1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "native-home"), []byte(home), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, title TEXT, cwd TEXT, started_at REAL)`); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if _, err := db.Exec(`INSERT INTO sessions (id, title, cwd, started_at) VALUES (?,?,?,?)`,
			row[0], row[1], row[2], float64(10+i)); err != nil {
			t.Fatal(err)
		}
	}
	return data, home
}

func nativeTitle(t *testing.T, data string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(sessionDir(data, "session-1"), "native-title"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPrepareNativeTerminalResumeMaterializesTitleForExactSessionAndWorkspace(t *testing.T) {
	data, _ := seedTerminalResumeHome(t, [3]string{"chat-1", "Curious Otter", "/ws"})
	p := New()
	found, err := p.PrepareNativeTerminalResume(context.Background(), data, "session-1", "chat-1", "/ws")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("the exact conversation in its own workspace was not resolved")
	}
	if got := nativeTitle(t, data); got != "Curious Otter" {
		t.Fatalf("native title = %q, want the conversation's own title", got)
	}
}

func TestPrepareNativeTerminalResumeRefusesForeignWorkspace(t *testing.T) {
	data, _ := seedTerminalResumeHome(t, [3]string{"chat-1", "Curious Otter", "/other"})
	p := New()
	found, err := p.PrepareNativeTerminalResume(context.Background(), data, "session-1", "chat-1", "/ws")
	if err != nil || found {
		t.Fatalf("found = %v err = %v, want an unresolved foreign-workspace conversation", found, err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir(data, "session-1"), "native-title")); !os.IsNotExist(err) {
		t.Fatalf("a foreign conversation's title was persisted: %v", err)
	}
}

func TestPrepareNativeTerminalResumeRefusesAmbiguousTitle(t *testing.T) {
	// "older" carries the duplicate title first, so restoring by title would
	// resolve to it, not to the recorded chat-2: refuse instead of resuming a
	// stranger's transcript.
	data, _ := seedTerminalResumeHome(t,
		[3]string{"older", "Dup", "/ws"},
		[3]string{"chat-2", "Dup", "/ws"},
	)
	p := New()
	found, err := p.PrepareNativeTerminalResume(context.Background(), data, "session-1", "chat-2", "/ws")
	if err != nil || found {
		t.Fatalf("found = %v err = %v, want an unresolved ambiguous title", found, err)
	}
}

func TestPrepareNativeTerminalResumeRefusesUnknownOrUntitledConversation(t *testing.T) {
	data, _ := seedTerminalResumeHome(t,
		[3]string{"chat-1", "", "/ws"},
	)
	p := New()
	for _, id := range []string{"missing", "chat-1", "latest", ""} {
		found, err := p.PrepareNativeTerminalResume(context.Background(), data, "session-1", id, "/ws")
		if err != nil || found {
			t.Fatalf("id %q: found = %v err = %v, want unresolved", id, found, err)
		}
	}
}

func TestPrepareNativeTerminalResumeRefusesWithoutRecordedNativeHome(t *testing.T) {
	data := t.TempDir()
	if err := os.MkdirAll(sessionDir(data, "session-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := New()
	found, err := p.PrepareNativeTerminalResume(context.Background(), data, "session-1", "chat-1", "/ws")
	if err != nil || found {
		t.Fatalf("found = %v err = %v, want a fresh start, not a failure", found, err)
	}
}
