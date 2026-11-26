package opencodev2

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	// The copy here is read before it is deleted, so a failed import can put it back.
	if len(lines) != 4 || lines[1] != "session export "+id+" --standalone" || lines[2] != "session delete "+id+" --standalone" ||
		!strings.HasPrefix(lines[3], "session import ") || !strings.HasSuffix(lines[3], " --directory "+cwd+" --standalone") {
		t.Fatalf("calls = %q", lines)
	}
}

// A moved conversation OpenCode will not import never costs the copy that was
// here: it is put back, where it ran, and the import reports the failure.
func TestConversationImportPutsTheEarlierCopyBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	const id = "ses_e34d0cd52ffeMzGW05mHN4RIm7"
	earlier := `{"info":{"id":"` + id + `","location":{"directory":"/w/app-earlier"}},"messages":["earlier"]}`
	moved := `{"info":{"id":"` + id + `","location":{"directory":"/w/app"}},"messages":["BROKEN"]}`
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	// OpenCode's store, as one file: delete removes the session, import adds it.
	store := filepath.Join(dir, "store.json")
	if err := os.WriteFile(store, []byte(earlier), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 2.0.0; exit; fi\necho \"$*\" >> " + log + "\n" +
		"if [ \"$2\" = export ]; then if [ -f " + store + " ]; then cat " + store + "; exit; fi; echo \"Session not found: $3\" >&2; exit 1; fi\n" +
		"if [ \"$2\" = delete ]; then rm -f " + store + "; fi\n" +
		"if [ \"$2\" = import ] && grep -q BROKEN \"$3\"; then echo 'invalid session' >&2; exit 1; fi\n" +
		"if [ \"$2\" = import ]; then cp \"$3\" " + filepath.Join(dir, "restored.json") + "; cp \"$3\" " + store + "; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := New().ImportConversation(context.Background(), []byte(moved), t.TempDir(), nil); err == nil {
		t.Fatal("a refused import reported success")
	}
	calls, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	// The backup, the delete, the refused import, a check that the copy is gone, then the backup put back.
	if len(lines) != 5 || lines[1] != "session delete "+id+" --standalone" || lines[3] != "session export "+id+" --standalone" ||
		!strings.HasSuffix(lines[4], " --directory /w/app-earlier --standalone") {
		t.Fatalf("calls = %q, want the earlier copy imported back where it ran", lines)
	}
	if restored, err := os.ReadFile(filepath.Join(dir, "restored.json")); err != nil || string(restored) != earlier {
		t.Fatalf("restored = %q, %v; want the earlier copy", restored, err)
	}
}

// The copy here is deleted only once it is backed up. OpenCode's own
// "Session not found" means there is none; any other failure to export it, or
// an export that is not this session, leaves it alone and refuses the import.
func TestConversationImportReplacesTheCopyHereOnlyOnceItIsBackedUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	const id = "ses_e34d0cd52ffeMzGW05mHN4RIm7"
	moved := `{"info":{"id":"` + id + `","location":{"directory":"/w/app"}},"messages":[]}`
	for _, tc := range []struct {
		name   string
		export string // the shell body that answers `session export`
		wantOK bool
		want   []string // the calls after the export, by subcommand
	}{
		{
			name:   "absent",
			export: `echo "Session not found: ` + id + `" >&2; exit 1`,
			wantOK: true, want: []string{"import"},
		},
		{
			name:   "store unreadable",
			export: `echo "database is locked" >&2; exit 1`,
		},
		{
			name:   "another session's export",
			export: `printf '%s' '{"info":{"id":"ses_other"},"messages":[]}'`,
		},
		{
			name:   "not found for another id",
			export: `echo "Session not found: ses_other" >&2; exit 1`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, log := t.TempDir(), filepath.Join(t.TempDir(), "calls")
			script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 2.0.0; exit; fi\necho \"$2\" >> " + log + "\n" +
				"if [ \"$2\" = export ]; then " + tc.export + "; fi\n"
			if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			got, err := New().ImportConversation(context.Background(), []byte(moved), t.TempDir(), nil)
			if tc.wantOK != (err == nil) || (tc.wantOK && got != id) {
				t.Fatalf("ImportConversation = %q, %v; want ok %v", got, err, tc.wantOK)
			}
			calls, _ := os.ReadFile(log)
			lines := strings.Fields(string(calls))
			if len(lines) == 0 || lines[0] != "export" {
				t.Fatalf("calls = %q, want the export first", lines)
			}
			if after := lines[1:]; strings.Join(after, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("calls after the export = %q, want %q", after, tc.want)
			}
		})
	}
}

// The copy deleted to make way for an import goes back whenever the import
// does not land: also when the move is cancelled mid-import, and when the
// delete fails after it removed the copy. A delete that removed nothing
// leaves the copy where it is.
func TestConversationImportPutsTheCopyBackWhateverStopsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	const id = "ses_e34d0cd52ffeMzGW05mHN4RIm7"
	earlier := `{"info":{"id":"` + id + `","location":{"directory":"/w/app-earlier"}},"messages":["earlier"]}`
	moved := `{"info":{"id":"` + id + `","location":{"directory":"/w/app"}},"messages":["moved"]}`
	for _, tc := range []struct {
		name string
		// delete and import are the shell bodies that answer those commands;
		// $S is OpenCode's store.
		delete, importMoved string
		cancelOnDelete      bool
		wantRestored        bool
	}{
		{
			name:   "cancelled during the import",
			delete: `rm -f "$S"; touch "$S.deleted"`, importMoved: `exec sleep 30`,
			cancelOnDelete: true, wantRestored: true,
		},
		{
			name:   "delete removed it, then failed",
			delete: `rm -f "$S"; echo "store closed" >&2; exit 1`, importMoved: `exit 0`,
			wantRestored: true,
		},
		{
			name:   "delete failed before removing it",
			delete: `echo "database is locked" >&2; exit 1`, importMoved: `exit 0`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := filepath.Join(dir, "store.json")
			if err := os.WriteFile(store, []byte(earlier), 0o600); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nS=" + store + "\nif [ \"$1\" = --version ]; then echo 2.0.0; exit; fi\n" +
				"if [ \"$2\" = export ]; then if [ -f \"$S\" ]; then cat \"$S\"; exit; fi; echo \"Session not found: $3\" >&2; exit 1; fi\n" +
				"if [ \"$2\" = delete ]; then " + tc.delete + "; fi\n" +
				// The moved document's import does as the case says; the backup's import restores it.
				"if [ \"$2\" = import ] && grep -q moved \"$3\"; then " + tc.importMoved + "; fi\n" +
				"if [ \"$2\" = import ] && grep -q earlier \"$3\"; then cp \"$3\" \"$S\"; touch \"$S.restored\"; fi\n"
			if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelOnDelete {
				// The move is cancelled once the copy is deleted, while the import runs.
				go func() {
					for range 2000 {
						if _, err := os.Stat(store + ".deleted"); err == nil {
							time.Sleep(200 * time.Millisecond)
							cancel()
							return
						}
						time.Sleep(5 * time.Millisecond)
					}
				}()
			}
			if _, err := New().ImportConversation(ctx, []byte(moved), t.TempDir(), nil); err == nil {
				t.Fatal("an import that did not land reported success")
			}
			got, err := os.ReadFile(store)
			if err != nil {
				t.Fatalf("the copy from before is gone: %v", err)
			}
			if string(got) != earlier {
				t.Fatalf("store = %q, want the copy from before", got)
			}
			_, err = os.Stat(store + ".restored")
			if restored := err == nil; restored != tc.wantRestored {
				t.Fatalf("restored = %v, want %v", restored, tc.wantRestored)
			}
		})
	}
}
