package sessionartifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCleanImportPath(t *testing.T) {
	for _, ok := range []string{"page.html", "shared/Roadmap/page.html", "shared/r/images/a b.png"} {
		if got, valid := CleanImportPath(ok); !valid || got != ok {
			t.Errorf("CleanImportPath(%q) = %q, %v; want it accepted as is", ok, got, valid)
		}
	}
	for _, bad := range []string{"", "/abs.html", "../up.html", "a/../b.html", "a//b.html", "a/./b.html", "a\\b.html", ".hidden", "a/.git/config", ".versions/x", "a/", "."} {
		if got, valid := CleanImportPath(bad); valid {
			t.Errorf("CleanImportPath(%q) = %q, accepted; want refused", bad, got)
		}
	}
}

func TestWriteImportCreatesFoldersAndReplacesFiles(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "shared", "r", "page.html")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteImport(dir, []domain.ArtifactImportFile{
		{Path: "shared/r/page.html", Data: []byte("new")},
		{Path: "shared/r/images/logo.png", Data: []byte{0x89, 'P', 'N', 'G'}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(existing); string(got) != "new" {
		t.Fatalf("page = %q, want replaced", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "shared", "r", "images", "logo.png")); len(got) != 4 {
		t.Fatalf("logo = %v, want written", got)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "shared", "r"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "page.html" && entry.Name() != "images" {
			t.Fatalf("left behind %q", entry.Name())
		}
	}
}

func TestWriteImportRefusesAFolderInTheWay(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "page.html"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteImport(dir, []domain.ArtifactImportFile{{Path: "page.html", Data: []byte("x")}}); err == nil {
		t.Fatal("WriteImport over a folder succeeded, want an error")
	}
}

func TestWriteImportNeedsADirectory(t *testing.T) {
	if err := WriteImport("", []domain.ArtifactImportFile{{Path: "a.html", Data: []byte("x")}}); err == nil {
		t.Fatal("WriteImport with no directory succeeded, want an error")
	}
}

func TestWriteImportIsAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("shared/r/page.html", "old page")
	write("shared/r/style.css", "old style")
	// Another program holds style.css: renaming the new one over it fails.
	realLink := linkInRoot
	t.Cleanup(func() { linkInRoot = realLink })
	linkInRoot = func(root *os.Root, from, to string) error {
		if to == filepath.Join("shared", "r", "style.css") && strings.Contains(from, "ao-import") {
			return os.ErrPermission
		}
		return realLink(root, from, to)
	}
	realRename := renameInRoot
	t.Cleanup(func() { renameInRoot = realRename })
	renameInRoot = func(root *os.Root, from, to string) error {
		if to == filepath.Join("shared", "r", "style.css") && strings.Contains(from, "ao-import") {
			return os.ErrPermission
		}
		return realRename(root, from, to)
	}
	err := WriteImport(dir, []domain.ArtifactImportFile{
		{Path: "shared/r/page.html", Data: []byte("new page")},
		{Path: "shared/r/new.png", Data: []byte("png")},
		{Path: "shared/r/style.css", Data: []byte("new style")},
	})
	if err == nil {
		t.Fatal("WriteImport succeeded, want the failed swap reported")
	}
	for name, want := range map[string]string{"page.html": "old page", "style.css": "old style"} {
		if got, _ := os.ReadFile(filepath.Join(dir, "shared", "r", name)); string(got) != want {
			t.Errorf("%s = %q, want %q put back", name, got, want)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "shared", "r"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "page.html" && entry.Name() != "style.css" {
			t.Errorf("left behind %q", entry.Name())
		}
	}
}

func TestWriteImportWritesNothingWhenAFileCannotBeStaged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "in-the-way"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := WriteImport(dir, []domain.ArtifactImportFile{
		{Path: "page.html", Data: []byte("new")},
		{Path: "in-the-way", Data: []byte("x")},
	})
	if err == nil {
		t.Fatal("WriteImport succeeded, want an error")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "page.html")); string(got) != "old" {
		t.Fatalf("page = %q, want untouched", got)
	}
}

func TestWriteImportLeavesAGuardedFileTheAgentChanged(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "page.html")
	if err := os.WriteFile(page, []byte("agent edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("what was kept"))
	err := WriteImport(dir, []domain.ArtifactImportFile{
		{Path: "new.css", Data: []byte("css"), Guard: true},
		{Path: "page.html", Data: []byte("owner v2"), Guard: true, Kept: hex.EncodeToString(sum[:])},
	})
	if !errors.Is(err, ErrImportFileChanged) {
		t.Fatalf("WriteImport = %v, want ErrImportFileChanged", err)
	}
	if got, _ := os.ReadFile(page); string(got) != "agent edit" {
		t.Fatalf("page = %q, want the agent's edit kept", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.css")); !os.IsNotExist(err) {
		t.Fatalf("new.css written anyway: %v", err)
	}
}

func TestWriteImportReplacesAGuardedFileThatStillHoldsWhatWasKept(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("v1"))
	if err := WriteImport(dir, []domain.ArtifactImportFile{{Path: "page.html", Data: []byte("v2"), Guard: true, Kept: hex.EncodeToString(sum[:])}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "page.html")); string(got) != "v2" {
		t.Fatalf("page = %q, want replaced", got)
	}
}

func TestWriteImportRecoversWhatAnInterruptedImportLeft(t *testing.T) {
	dir := t.TempDir()
	// The daemon stopped mid-swap: the original only at its backup, a temp beside it.
	if err := os.WriteFile(filepath.Join(dir, ".page.html.ao-backup-0011223344556677"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".page.html.ao-import-8899aabbccddeeff"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("original"))
	if err := WriteImport(dir, []domain.ArtifactImportFile{{Path: "page.html", Data: []byte("new"), Guard: true, Kept: hex.EncodeToString(sum[:])}}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "page.html" {
		t.Fatalf("left = %v, want only page.html", entries)
	}
}

func TestWriteImportNamesWhatItCouldNotUndo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.html"), []byte("old a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.css"), []byte("old b"), 0o644); err != nil {
		t.Fatal(err)
	}
	// b.css cannot be swapped in, and a.html's original cannot be put back.
	fail := func(from, to string) bool {
		return (to == "b.css" && strings.Contains(from, "ao-import")) || (to == "a.html" && strings.Contains(from, "ao-backup"))
	}
	realLink, realRename := linkInRoot, renameInRoot
	t.Cleanup(func() { linkInRoot, renameInRoot = realLink, realRename })
	linkInRoot = func(root *os.Root, from, to string) error {
		if fail(from, to) {
			return os.ErrPermission
		}
		return realLink(root, from, to)
	}
	renameInRoot = func(root *os.Root, from, to string) error {
		if fail(from, to) {
			return os.ErrPermission
		}
		return realRename(root, from, to)
	}
	err := WriteImport(dir, []domain.ArtifactImportFile{{Path: "a.html", Data: []byte("new a")}, {Path: "b.css", Data: []byte("new b")}})
	if !errors.Is(err, ErrImportIncomplete) || errors.Is(err, ErrImportFileChanged) {
		t.Fatalf("WriteImport = %v, want ErrImportIncomplete", err)
	}
	if !strings.Contains(err.Error(), "a.html") || !strings.Contains(err.Error(), "ao-backup") {
		t.Fatalf("error %q does not say where a.html's original is", err)
	}
}

func TestMarkSharedCopyHidesItsMarkerFromTheList(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shared", "r"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MarkSharedCopy(dir, "shared/r", []domain.ArtifactImportFile{{Path: "shared/r/a.png", Data: []byte("a")}}); err != nil {
		t.Fatal(err)
	}
	// An update adds its files' sums to those already listed.
	if err := MarkSharedCopy(dir, "shared/r", []domain.ArtifactImportFile{{Path: "shared/r/a.png", Data: []byte("a2")}}); err != nil {
		t.Fatal(err)
	}
	listed, _ := os.ReadFile(filepath.Join(dir, "shared", "r", SharedCopyMarker))
	for _, data := range []string{"a", "a2"} {
		sum := sha256.Sum256([]byte(data))
		if !strings.Contains(string(listed), hex.EncodeToString(sum[:])) {
			t.Fatalf("marker %q does not list %q", listed, data)
		}
	}
	if CopyRoot("shared/r/pages/index.html") != "shared/r" || CopyRoot("report.html") != "" {
		t.Fatal("CopyRoot")
	}
	if _, err := os.Stat(filepath.Join(dir, "shared", "r", SharedCopyMarker)); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if !strings.HasPrefix(SharedCopyMarker, ".") {
		t.Fatal("the marker must be hidden, so the artifact list leaves it out")
	}
}

func TestWriteImportTidiesLeftoversBesideFilesItNoLongerWrites(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "shared", "r")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	// An interrupted import left old.css only at its backup, and a temp beside it.
	if err := os.WriteFile(filepath.Join(folder, ".old.css.ao-backup-0011223344556677"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, ".old.css.ao-import-8899aabbccddeeff"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteImport(dir, []domain.ArtifactImportFile{{Path: "shared/r/page.html", Data: []byte("new")}}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(folder)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "old.css,page.html" {
		t.Fatalf("folder = %v, want old.css back and page.html written", names)
	}
}

func TestWriteImportLeavesAFileWrittenWhileTheOriginalWasAside(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	realRename := renameInRoot
	t.Cleanup(func() { renameInRoot = realRename })
	renameInRoot = func(root *os.Root, from, to string) error {
		err := realRename(root, from, to)
		// An editor's autosave writes page.html again just after it is moved aside.
		if err == nil && from == "page.html" && strings.Contains(to, "ao-backup") {
			_ = os.WriteFile(filepath.Join(dir, "page.html"), []byte("autosaved"), 0o644)
		}
		return err
	}
	sum := sha256.Sum256([]byte("v1"))
	err := WriteImport(dir, []domain.ArtifactImportFile{{Path: "page.html", Data: []byte("v2"), Guard: true, Kept: hex.EncodeToString(sum[:])}})
	if !errors.Is(err, ErrImportFileChanged) && !errors.Is(err, ErrImportIncomplete) {
		t.Fatalf("WriteImport = %v, want the import refused", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "page.html")); string(got) != "autosaved" {
		t.Fatalf("page = %q, want the autosaved file left as it is", got)
	}
}

func TestWriteImportNeverUndoesOverASaveMadeAfterIt(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{"a.html": "old a", "b.css": "old b"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	realLink := linkInRoot
	t.Cleanup(func() { linkInRoot = realLink })
	linkInRoot = func(root *os.Root, from, to string) error {
		if to == "b.css" && strings.Contains(from, "ao-import") {
			// An editor saves a.html after it was updated; then b.css fails.
			_ = os.WriteFile(filepath.Join(dir, "a.html"), []byte("saved by editor"), 0o644)
			return os.ErrPermission
		}
		return realLink(root, from, to)
	}
	realRename := renameInRoot
	t.Cleanup(func() { renameInRoot = realRename })
	renameInRoot = func(root *os.Root, from, to string) error {
		if to == "b.css" && strings.Contains(from, "ao-import") {
			return os.ErrPermission
		}
		return realRename(root, from, to)
	}
	err := WriteImport(dir, []domain.ArtifactImportFile{{Path: "a.html", Data: []byte("new a")}, {Path: "b.css", Data: []byte("new b")}})
	if !errors.Is(err, ErrImportIncomplete) {
		t.Fatalf("WriteImport = %v, want ErrImportIncomplete naming a.html", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.html")); string(got) != "saved by editor" {
		t.Fatalf("a.html = %q, want the editor's save kept", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "b.css")); string(got) != "old b" {
		t.Fatalf("b.css = %q, want put back", got)
	}
}

func TestPlaceNewNeverReplacesAFile(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for name, data := range map[string]string{"from": "new", "to": "someone's save"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := placeNew(root, "from", "to"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("placeNew = %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "to")); string(got) != "someone's save" {
		t.Fatalf("to = %q, want left as it was", got)
	}
	if err := placeNew(root, "from", "free"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "from")); !os.IsNotExist(err) {
		t.Fatal("from was not moved")
	}
}
