package sessionartifacts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// identityDir is an artifact directory whose rows are applied in memory the
// way the store applies them, so a test can scan, change files and scan again.
type identityDir struct {
	t    *testing.T
	dir  string
	rows []domain.SessionArtifactRecord
	next int
}

func newIdentityDir(t *testing.T) *identityDir {
	t.Helper()
	d := &identityDir{t: t, dir: t.TempDir()}
	d.write("probe", "x")
	info, err := os.Lstat(filepath.Join(d.dir, "probe"))
	if err != nil {
		t.Fatal(err)
	}
	if !fullIdentity(filepath.Join(d.dir, "probe"), info).SameFile(fullIdentity(filepath.Join(d.dir, "probe"), info)) {
		t.Skip("this file system does not report file birth times")
	}
	d.remove("probe")
	return d
}

func (d *identityDir) write(rel, body string) {
	d.t.Helper()
	p := filepath.Join(d.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		d.t.Fatal(err)
	}
}

func (d *identityDir) rename(from, to string) {
	d.t.Helper()
	target := filepath.Join(d.dir, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(d.dir, filepath.FromSlash(from)), target); err != nil {
		d.t.Fatal(err)
	}
}

func (d *identityDir) remove(rel string) {
	d.t.Helper()
	if err := os.Remove(filepath.Join(d.dir, filepath.FromSlash(rel))); err != nil {
		d.t.Fatal(err)
	}
}

// scan observes the directory, applies the planned changes to the rows and
// returns the changes and the listed files.
func (d *identityDir) scan() (domain.SessionArtifactChanges, []domain.SessionArtifactFile) {
	d.t.Helper()
	observation, err := Observe(context.Background(), d.dir, RegisteredPaths(d.rows))
	if err != nil {
		d.t.Fatal(err)
	}
	changes := observation.Changes(d.rows, time.Now(), func() string {
		d.next++
		return fmt.Sprintf("id-%d", d.next)
	})
	byID := map[string]int{}
	for i, row := range d.rows {
		byID[row.ID] = i
	}
	dropped := map[string]bool{}
	for _, drop := range changes.Drop {
		dropped[drop.ID] = true
	}
	// The harness keeps no versions, so a forgotten row always goes.
	for _, forget := range changes.Forget {
		dropped[forget.ID] = true
	}
	for _, move := range changes.Move {
		row := &d.rows[byID[move.ID]]
		row.Path, row.File, row.PreviousPaths = move.To, move.File, move.PreviousPaths
		if row.RegisteredAt == nil {
			row.RegisteredAt = move.RegisteredAt
		}
	}
	for _, update := range changes.SetFile {
		d.rows[byID[update.ID]].File = update.File
	}
	d.rows = slices.DeleteFunc(d.rows, func(row domain.SessionArtifactRecord) bool { return dropped[row.ID] })
	d.rows = append(d.rows, changes.Insert...)
	seen := map[string]bool{}
	for _, row := range d.rows {
		if seen[row.Path] {
			d.t.Fatalf("two rows at %s: %+v", row.Path, d.rows)
		}
		seen[row.Path] = true
	}
	return changes, observation.Apply(d.rows)
}

func (d *identityDir) register(path string) {
	now := time.Now()
	for i := range d.rows {
		if d.rows[i].Path == path {
			d.rows[i].RegisteredAt = &now
			return
		}
	}
	d.t.Fatalf("no row at %s", path)
}

func (d *identityDir) idAt(path string) string {
	d.t.Helper()
	for _, row := range d.rows {
		if row.Path == path {
			return row.ID
		}
	}
	d.t.Fatalf("no row at %s: %+v", path, d.rows)
	return ""
}

func fileAt(files []domain.SessionArtifactFile, path string) (domain.SessionArtifactFile, bool) {
	for _, file := range files {
		if file.Path == path {
			return file, true
		}
	}
	return domain.SessionArtifactFile{}, false
}

func TestRenamedFileKeepsItsIdentity(t *testing.T) {
	d := newIdentityDir(t)
	d.write("report.html", "<p>draft</p>")
	changes, _ := d.scan()
	if len(changes.Insert) != 1 {
		t.Fatalf("first scan = %+v, want one new row", changes)
	}
	id := d.idAt("report.html")
	d.register("report.html")
	if changes, _ := d.scan(); !changes.Empty() {
		t.Fatalf("unchanged scan = %+v", changes)
	}

	d.rename("report.html", "final/report-v2.html")
	changes, files := d.scan()
	if len(changes.Move) != 1 || len(changes.Insert) != 0 || changes.Move[0].From != "report.html" || changes.Move[0].To != "final/report-v2.html" {
		t.Fatalf("rename = %+v, want one move", changes)
	}
	file, ok := fileAt(files, "final/report-v2.html")
	if !ok || file.ID != id || !file.Registered || !slices.Equal(file.PreviousPaths, []string{"report.html"}) {
		t.Fatalf("renamed file = %+v, want id %s, registered, previous report.html", file, id)
	}
	if changes, _ := d.scan(); !changes.Empty() {
		t.Fatalf("scan after the rename = %+v", changes)
	}
}

func TestCopyAndNewFileAreNewArtifacts(t *testing.T) {
	d := newIdentityDir(t)
	d.write("a.html", "a")
	d.scan()
	id := d.idAt("a.html")
	d.write("copy.html", "a")
	changes, _ := d.scan()
	if len(changes.Move) != 0 || len(changes.Insert) != 1 || d.idAt("a.html") != id {
		t.Fatalf("copy = %+v", changes)
	}
	// A file deleted and another written elsewhere is not a rename, even if
	// the new file gets the old inode number.
	d.remove("a.html")
	d.write("other.html", "b")
	changes, _ = d.scan()
	if len(changes.Move) != 0 || d.idAt("other.html") == id {
		t.Fatalf("delete and write = %+v", changes)
	}
}

func TestFileWrittenAgainInPlaceKeepsItsIdentity(t *testing.T) {
	d := newIdentityDir(t)
	d.write("page.html", "one")
	d.scan()
	id := d.idAt("page.html")
	// An editor that saves through a new file and a rename over the old one.
	d.write("page.html.tmp", "two")
	d.rename("page.html.tmp", "page.html")
	d.scan()
	if d.idAt("page.html") != id {
		t.Fatal("a file replaced in place got a new identity")
	}
	// And an agent that deletes and writes it again: a registered file keeps
	// its row while it is gone (an unregistered one does if it has history).
	d.register("page.html")
	d.remove("page.html")
	d.scan()
	d.write("page.html", "three")
	d.scan()
	if d.idAt("page.html") != id {
		t.Fatal("a file deleted and written again got a new identity")
	}
}

func TestRenameOverAnotherFileKeepsTheDestination(t *testing.T) {
	d := newIdentityDir(t)
	d.write("draft.html", "new")
	d.write("report.html", "old")
	d.scan()
	report := d.idAt("report.html")
	d.rename("draft.html", "report.html")
	changes, _ := d.scan()
	if len(changes.Move) != 0 || d.idAt("report.html") != report {
		t.Fatalf("move over a file = %+v, want report.html to keep its artifact", changes)
	}
}

func TestRenameOntoAGoneFileTakesItsPath(t *testing.T) {
	d := newIdentityDir(t)
	d.write("a.html", "a")
	d.write("b.html", "b")
	d.scan()
	a, b := d.idAt("a.html"), d.idAt("b.html")
	d.register("b.html")
	d.remove("b.html")
	d.scan()
	d.rename("a.html", "b.html")
	changes, _ := d.scan()
	if len(changes.Move) != 1 || len(changes.Drop) != 1 || changes.Drop[0].ID != b || d.idAt("b.html") != a {
		t.Fatalf("rename onto a gone file = %+v", changes)
	}
}

func TestSwappedFilesKeepTheirIdentities(t *testing.T) {
	d := newIdentityDir(t)
	d.write("a.html", "a")
	d.write("b.html", "b")
	d.scan()
	a, b := d.idAt("a.html"), d.idAt("b.html")
	d.rename("a.html", "tmp.html")
	d.rename("b.html", "a.html")
	d.rename("tmp.html", "b.html")
	changes, _ := d.scan()
	if len(changes.Move) != 2 || d.idAt("b.html") != a || d.idAt("a.html") != b {
		t.Fatalf("swap = %+v", changes)
	}
}

func TestRenamedDirectoryMovesEveryFile(t *testing.T) {
	d := newIdentityDir(t)
	d.write("brand.icon/icon.json", "{}")
	d.write("brand.icon/Assets/a.png", "png")
	d.write("assets/logo.png", "logo")
	d.scan()
	bundle, logo := d.idAt("brand.icon"), d.idAt("assets/logo.png")
	d.register("brand.icon")
	if err := os.Rename(filepath.Join(d.dir, "brand.icon"), filepath.Join(d.dir, "app.icon")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(d.dir, "assets"), filepath.Join(d.dir, "images")); err != nil {
		t.Fatal(err)
	}
	_, files := d.scan()
	if d.idAt("app.icon") != bundle || d.idAt("images/logo.png") != logo {
		t.Fatalf("rows after renaming directories = %+v", d.rows)
	}
	if file, ok := fileAt(files, "app.icon/icon.json"); !ok || !file.Registered {
		t.Fatalf("file in the renamed bundle = %+v, want registered", file)
	}
}

func TestPreviousPathsAreBounded(t *testing.T) {
	got := []string{}
	for i := range MaxPreviousPaths + 5 {
		got = previousPaths(got, fmt.Sprintf("p%d", i), fmt.Sprintf("p%d", i+1))
	}
	if len(got) != MaxPreviousPaths || got[len(got)-1] != fmt.Sprintf("p%d", MaxPreviousPaths+4) {
		t.Fatalf("previous paths = %v", got)
	}
	if got := previousPaths([]string{"a", "b"}, "c", "a"); !slices.Equal(got, []string{"b", "c"}) {
		t.Fatalf("moving back to a previous path = %v", got)
	}
}

func TestRenamedAndEditedFileKeepsItsIdentity(t *testing.T) {
	d := newIdentityDir(t)
	d.write("notes.md", "one")
	d.scan()
	id := d.idAt("notes.md")
	if d.rows[0].File.ModTime != 0 {
		t.Skip("this file system has no inode generation, so a rename and an edit look like a new file")
	}
	d.rename("notes.md", "summary.md")
	d.write("summary.md", "one, then more")
	if d.scan(); d.idAt("summary.md") != id {
		t.Fatal("a file renamed and edited before the scan got a new identity")
	}
}

// A rename onto the path of a registered file that is gone keeps the path a
// deliverable: the renamed artifact takes its registration.
func TestRenameOntoAGoneRegisteredFileKeepsTheRegistration(t *testing.T) {
	d := newIdentityDir(t)
	d.write("report.html", "old")
	d.write("draft.html", "new")
	d.scan()
	d.register("report.html")
	d.remove("report.html")
	d.scan()
	draft := d.idAt("draft.html")
	d.rename("draft.html", "report.html")
	changes, _ := d.scan()
	if len(changes.Move) != 1 || changes.Move[0].RegisteredAt == nil || len(changes.Drop) != 1 {
		t.Fatalf("rename onto a gone registered file = %+v", changes)
	}
	// Registered rows are read back for the listing, as the store does.
	files := observeFiles(d)
	if file, ok := fileAt(files, "report.html"); !ok || file.ID != draft || !file.Registered {
		t.Fatalf("report.html = %+v, want draft's artifact, registered", file)
	}
}

// A gone file that was never registered is forgotten, so transient files such
// as an editor's do not leave rows behind; a registered one is kept.
func TestGoneUnregisteredFileIsForgotten(t *testing.T) {
	d := newIdentityDir(t)
	d.write("page.html.tmp", "x")
	d.write("report.html", "r")
	d.scan()
	d.register("report.html")
	d.remove("page.html.tmp")
	d.remove("report.html")
	changes, _ := d.scan()
	if len(changes.Forget) != 1 || changes.Forget[0].Path != "page.html.tmp" || len(changes.SetFile) != 1 || changes.SetFile[0].Path != "report.html" {
		t.Fatalf("gone files = %+v, want the temporary file forgotten and report.html kept", changes)
	}
}

func observeFiles(d *identityDir) []domain.SessionArtifactFile {
	d.t.Helper()
	observation, err := Observe(context.Background(), d.dir, RegisteredPaths(d.rows))
	if err != nil {
		d.t.Fatal(err)
	}
	return observation.Apply(d.rows)
}
