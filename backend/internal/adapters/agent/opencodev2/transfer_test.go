package opencodev2

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A moved conversation replaces the copy left here: OpenCode refuses to
// import an id it has, so the import deletes it first.
func TestConversationExportAndImport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	const id = "ses_e34d0cd52ffeMzGW05mHN4RIm7"
	doc := `{"info":{"id":"` + id + `","location":{"directory":"/w/app"}},"messages":[]}`
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 2.0.0; exit; fi\necho \"$*\" >> " + log + "\nif [ \"$2\" = export ]; then printf '%s' '" + doc + "'; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	p := New()

	got, err := p.ExportConversation(context.Background(), id, nil)
	if err != nil || string(got) != doc {
		t.Fatalf("ExportConversation = %s, %v", got, err)
	}
	if _, err := p.ExportConversation(context.Background(), "../x", nil); err == nil {
		t.Fatal("an id that is not a session id was exported")
	}
	cwd := t.TempDir()
	if got, err := p.ImportConversation(context.Background(), []byte(doc), cwd, nil); err != nil || got != id {
		t.Fatalf("ImportConversation = %q, %v", got, err)
	}
	calls, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 3 || lines[1] != "session delete "+id+" --standalone" ||
		!strings.HasPrefix(lines[2], "session import ") || !strings.HasSuffix(lines[2], " --directory "+cwd+" --standalone") {
		t.Fatalf("calls = %q", lines)
	}
}
