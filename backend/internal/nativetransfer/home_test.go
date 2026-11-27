package nativetransfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/pkg/handoffnote"
)

// The tests run without a home folder unless one sets it, so a temp folder
// under the real home folder is not rewritten behind their backs.
func TestMain(m *testing.M) {
	userHome = func() string { return "" }
	os.Exit(m.Run())
}

func withHome(t *testing.T, home string) {
	t.Helper()
	prev := userHome
	userHome = func() string { return home }
	t.Cleanup(func() { userHome = prev })
}

func TestCloudHomePlaceholderMatchesTheHandoffNote(t *testing.T) {
	if CloudHomePlaceholder != handoffnote.HomePlaceholder {
		t.Fatalf("placeholders differ: %q and %q", CloudHomePlaceholder, handoffnote.HomePlaceholder)
	}
}

// A transcript moved to AO Cloud names no path under the user's home folder:
// the working directory and the Claude project folder get their own
// placeholders, every other path under home gets CloudHomePlaceholder, and a
// path that only shares the home folder's prefix stays. Moving it back fills
// the placeholder in with this machine's home folder.
func TestCloudTranscriptHidesTheHomeFolderAndComesBackFilled(t *testing.T) {
	f := newClaudeFixture(t)
	home := filepath.Dir(filepath.Dir(filepath.Dir(f.cwdA))) // .../alice
	withHome(t, home)
	line := `{"type":"user","cwd":"` + f.cwdA + `","message":{"role":"user","content":"read ` + home + `/.ssh/config, ` + home + ` and ` + home + `x/y"}}`
	transcript := filepath.Join(f.projA, testID+".jsonl")
	data, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, transcript, string(data)+line+"\n")

	var out bytes.Buffer
	if _, err := WriteClaudeTranscriptForCloud(context.Background(), &out, ClaudeExportOptions{ConfigDir: f.cfgA, NativeID: testID, Cwd: f.cwdA}, 1<<20); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`"cwd":"` + CloudCwdPlaceholder + `"`,
		"read " + CloudHomePlaceholder + "/.ssh/config, " + CloudHomePlaceholder + " and " + home + "x/y",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript lacks %s:\n%s", want, got)
		}
	}
	// Only the signed thinking block, and the prefix-only path, may still name home.
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.Contains(strings.ReplaceAll(l, "/prefix"+home, ""), home+"/") && !strings.Contains(l, `"thinking"`) {
			t.Errorf("line still names the home folder: %s", l)
		}
	}

	var sidecar bytes.Buffer
	if ok, err := WriteClaudeSidecarForCloud(context.Background(), &sidecar, ClaudeExportOptions{ConfigDir: f.cfgA, NativeID: testID, Cwd: f.cwdA}, 1<<20); err != nil || !ok {
		t.Fatalf("sidecar = %v, %v", ok, err)
	}
	if entries := sidecarEntries(t, sidecar.Bytes()); strings.Contains(entries["subagents/agent-1.jsonl"], home) {
		t.Errorf("sidecar still names the home folder: %v", entries)
	}

	// Back on a machine whose home folder is another one.
	here := t.TempDir()
	withHome(t, here)
	inbox := filepath.Join(t.TempDir(), "in")
	writeFile(t, filepath.Join(inbox, testID+".jsonl"), got)
	if err := ExtractClaudeSidecar(bytes.NewReader(sidecar.Bytes()), filepath.Join(inbox, testID), 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := FillCloudHome(filepath.Join(inbox, testID+".jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := FillCloudHome(filepath.Join(inbox, testID)); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(filepath.Join(inbox, testID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), "read "+here+"/.ssh/config, "+here+" and ") || strings.Contains(string(back), CloudHomePlaceholder) {
		t.Errorf("placeholder not filled in with %s:\n%s", here, back)
	}
	if got := FillCloudHomeText("see " + CloudHomePlaceholder + "/notes and " + CloudHomePlaceholder + "x"); got != "see "+here+"/notes and "+CloudHomePlaceholder+"x" {
		t.Errorf("FillCloudHomeText = %q", got)
	}
}

func TestOpenCodeExportHidesTheHomeFolderAndComesBackFilled(t *testing.T) {
	withHome(t, "/Users/ann")
	doc := `{"info":{"id":"ses_1","location":{"directory":"/Users/ann/wt/app"}},"messages":[{"text":"see /Users/ann/.config/x and /Users/ann/wt/app/a.go"}]}`
	out, err := RewriteOpenCodeExport([]byte(doc), CloudCwdPlaceholder)
	if err != nil {
		t.Fatal(err)
	}
	if want := `see /ao-moved/home/.config/x and /ao-moved/cwd/a.go`; !strings.Contains(string(out), want) || strings.Contains(string(out), "/Users/ann") {
		t.Fatalf("export = %s, want %s", out, want)
	}
	withHome(t, "/home/bob")
	back, err := RewriteOpenCodeExport(out, "/home/bob/wt/app-2")
	if err != nil {
		t.Fatal(err)
	}
	if want := `see /home/bob/.config/x and /home/bob/wt/app-2/a.go`; !strings.Contains(string(back), want) {
		t.Fatalf("import = %s, want %s", back, want)
	}
}

func TestExtractClaudeSidecarRefusesTooManyFiles(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for i := 0; i <= MaxSidecarEntries; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("tool-results/%05d.txt", i), Typeflag: tar.TypeReg, Mode: 0o600}); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	dir := t.TempDir()
	if err := ExtractClaudeSidecar(&buf, dir, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}
