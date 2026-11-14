package sessionartifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDir_JoinsDataDirArtifactsAndID(t *testing.T) {
	got := Dir("/data", domain.SessionID("mer-1"))
	want := filepath.Join("/data", "artifacts", "mer-1")
	if got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}

func TestDir_EmptyDataDirReturnsEmpty(t *testing.T) {
	if got := Dir("", domain.SessionID("mer-1")); got != "" {
		t.Fatalf("Dir with empty dataDir = %q, want empty", got)
	}
	if got := Dir("   ", domain.SessionID("mer-1")); got != "" {
		t.Fatalf("Dir with whitespace-only dataDir = %q, want empty", got)
	}
}

func TestList_ClassifiesByExtensionAndSortsByPath(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"b.html": "<p>x</p>", "a.md": "# x", "c.bin": "x", "noext": "<!doctype html><html></html>"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]domain.SessionArtifactKind{}
	var paths []string
	for _, f := range files {
		got[f.Path] = f.Kind
		paths = append(paths, f.Path)
	}
	if !sort.StringsAreSorted(paths) || len(paths) != 4 {
		t.Fatalf("paths = %v, want 4 sorted", paths)
	}
	if got["b.html"] != domain.SessionArtifactHTML || got["a.md"] != domain.SessionArtifactMarkdown || got["c.bin"] != domain.SessionArtifactGeneric || got["noext"] != domain.SessionArtifactGeneric {
		t.Fatalf("kinds = %v", got)
	}
}

func TestList_SkipsOSMetadataAndToolFiles(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{
		"report.md",
		"site/index.html",
		".DS_Store",
		"site/.DS_Store",
		"._report.md",
		"Thumbs.db",
		"site/desktop.ini",
		".git/HEAD",
		".versions/blobs/x",
		"node_modules/pkg/index.js",
		"site/__pycache__/gen.pyc",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if fmt.Sprint(paths) != "[report.md site/index.html]" {
		t.Fatalf("paths = %v, want [report.md site/index.html]", paths)
	}
}

func TestSkips_AgreesWithList(t *testing.T) {
	dir := t.TempDir()
	paths := map[string]bool{
		"plan.md":                    false,
		"charts/q4.html":             false,
		"notes/.versions/kept.md":    false, // Only the top level holds versions.
		".versions/plan.md/1.txt":    true,
		"node_modules/rxjs/index.js": true,
		"charts/node_modules/x/a.js": true,
		"NODE_MODULES/x/a.js":        true,
		"app/__pycache__/mod.pyc":    true,
		"repo/.git/HEAD":             true,
		"notes/.DS_Store":            true,
		"notes/._plan.md":            true,
	}
	for rel := range paths {
		file := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listed := map[string]bool{}
	for _, file := range files {
		listed[file.Path] = true
	}
	for rel, skipped := range paths {
		if got := Skips(rel); got != skipped {
			t.Errorf("Skips(%q) = %v, want %v", rel, got, skipped)
		}
		if listed[rel] == skipped {
			t.Errorf("List has %q = %v, but Skips is meant to agree with it", rel, listed[rel])
		}
	}
}

func TestList_BoundsFileCount(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxFiles+25; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d.txt", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != MaxFiles {
		t.Fatalf("len = %d, want %d", len(files), MaxFiles)
	}
}

func TestList_SkipsUnreadableSubdirInsteadOfFailing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permissions are not enforced on Windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List returned error for unreadable subdir: %v", err)
	}
	if len(files) != 1 || files[0].Path != "ok.md" {
		t.Fatalf("files = %+v, want only ok.md", files)
	}
}

func TestList_BoundsTraversalOfEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxVisited+50; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("d%05d", i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "zzz.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %+v, want the traversal cut off before reaching zzz.txt", files)
	}
}

func TestList_SkipsTopLevelVersionsDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"page.html",
		filepath.Join(VersionsDir, "blobs", "ab", "abcd"),
		filepath.Join("nested", VersionsDir, "kept.md"),
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	want := []string{"nested/" + VersionsDir + "/kept.md", "page.html"}
	if fmt.Sprint(paths) != fmt.Sprint(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestList_StopsOnCancelledContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := List(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("List with a cancelled context = %v, want context.Canceled", err)
	}
}

func writeArtifact(t *testing.T, dir, rel string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrable(t *testing.T) {
	dir := t.TempDir()
	writeArtifact(t, dir, "brand.html")
	writeArtifact(t, dir, "icons/app.icon/icon.json")
	writeArtifact(t, dir, ".versions/x")
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeArtifact(t, filepath.Dir(outside), "outside.md")
	if err := os.Symlink(outside, filepath.Join(dir, "link.md")); err != nil {
		t.Logf("symlink unavailable: %v", err)
	}
	tests := []struct {
		name, reference, want string
		err                   error
	}{
		{name: "relative file", reference: "brand.html", want: "brand.html"},
		{name: "absolute file", reference: filepath.Join(dir, "brand.html"), want: "brand.html"},
		{name: "bundle directory", reference: "icons/app.icon", want: "icons/app.icon"},
		{name: "file in a bundle", reference: "icons/app.icon/icon.json", want: "icons/app.icon/icon.json"},
		{name: "plain directory", reference: "icons", err: ErrNotRegistrable},
		{name: "the directory itself", reference: dir, err: ErrNotRegistrable},
		{name: "versions directory", reference: ".versions/x", err: ErrNotRegistrable},
		{name: "symlink", reference: "link.md", err: ErrNotRegistrable},
		{name: "missing", reference: "gone.md", err: ErrNotFound},
		{name: "parent traversal", reference: "../outside.md", err: ErrOutsideDir},
		{name: "absolute outside", reference: outside, err: ErrOutsideDir},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Registrable(dir, tc.reference)
			if !errors.Is(err, tc.err) || got.Path != tc.want {
				t.Fatalf("Registrable(%q) = %q, %v, want %q, %v", tc.reference, got.Path, err, tc.want, tc.err)
			}
		})
	}
}

func registeredPaths(files []domain.SessionArtifactFile) []string {
	paths := []string{}
	for _, file := range files {
		if file.Registered {
			paths = append(paths, file.Path)
		}
	}
	return paths
}

func TestListRegisteredMarksFilesAndBundleContents(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"brand.html", "assets/a.png", "app.icon/icon.json", "app.icon/mark.svg"} {
		writeArtifact(t, dir, rel)
	}
	files, err := ListRegistered(context.Background(), dir, []string{"brand.html", "app.icon", "gone.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("files = %+v, want all 4", files)
	}
	want := []string{"app.icon/icon.json", "app.icon/mark.svg", "brand.html"}
	if got := registeredPaths(files); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("registered = %v, want %v", got, want)
	}
}

func TestListRegisteredKeepsADeliverablePastTheFileCap(t *testing.T) {
	dir := t.TempDir()
	for i := range MaxFiles + 5 {
		writeArtifact(t, dir, fmt.Sprintf("assets/%05d.png", i))
	}
	writeArtifact(t, dir, "brand.html")
	writeArtifact(t, dir, "z.icon/icon.json")

	plain, err := List(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != MaxFiles || plain[len(plain)-1].Path == "brand.html" {
		t.Fatalf("List reached past the cap: %d files", len(plain))
	}
	files, err := ListRegistered(context.Background(), dir, []string{"brand.html", "z.icon"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"brand.html", "z.icon/icon.json"}
	if got := registeredPaths(files); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("registered = %v, want %v", got, want)
	}
	if !sort.SliceIsSorted(files, func(i, j int) bool { return files[i].Path < files[j].Path }) {
		t.Fatal("files are not sorted by path")
	}
}
