package sessionartifacts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkedDir is an artifact directory and a symlink to it, the way macOS
// reaches /var through /private/var or a person links ~/.ao elsewhere.
func linkedDir(t *testing.T) (target, link string) {
	t.Helper()
	base := t.TempDir()
	target = filepath.Join(base, "real")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	return target, link
}

func TestRelPathReachesTheDirectoryThroughASymlink(t *testing.T) {
	target, link := linkedDir(t)
	writeArtifact(t, target, "report.html")
	for _, tc := range []struct{ name, dir, reference, want string }{
		{"directory named by its link", link, filepath.Join(target, "report.html"), "report.html"},
		{"file named through the link", target, filepath.Join(link, "report.html"), "report.html"},
		{"file not written yet", link, filepath.Join(target, "later", "page.html"), "later/page.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rel, ok := RelPath(tc.dir, tc.reference); !ok || rel != tc.want {
				t.Fatalf("RelPath(%q, %q) = %q, %v; want %q", tc.dir, tc.reference, rel, ok, tc.want)
			}
		})
	}
	got, err := Registrable(link, filepath.Join(target, "report.html"))
	if err != nil || got.Path != "report.html" {
		t.Fatalf("Registrable through link = %q, %v", got.Path, err)
	}
}

func TestRelPathStillRefusesASymlinkThatLeavesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeArtifact(t, outside, "secret.md")
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	// The path reads as inside, but the file sits behind a link that leaves
	// dir: os.Root refuses to follow it.
	if _, err := Registrable(dir, filepath.Join(dir, "escape", "secret.md")); err == nil {
		t.Fatal("registered a file reached through a link that leaves the directory")
	}
	if _, ok := RelPath(dir, filepath.Join(outside, "secret.md")); ok {
		t.Fatal("a file in another directory counted as inside")
	}
}

func TestRelUnderIgnoresLetterCaseOnlyWhereTheFileSystemDoes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Users", "Jane", ".ao", "data", "artifacts", "ao-1")
	reference := strings.Replace(dir, "Users", "users", 1) + string(filepath.Separator) + "report.html"
	if got := relUnder(dir, reference, true); got != "report.html" {
		t.Fatalf("folded = %q, want report.html", got)
	}
	if got := relUnder(dir, reference, false); got != ".." {
		t.Fatalf("case-sensitive = %q, want ..", got)
	}
	sibling := dir + "-other" + string(filepath.Separator) + "report.html"
	if got := relUnder(dir, sibling, true); got != ".." {
		t.Fatalf("a sibling directory with the same prefix counted as inside: %q", got)
	}
}

func TestWindowsDrivePath(t *testing.T) {
	for in, want := range map[string]string{
		"/c/Users/Jane/.ao/data/artifacts/ao-1/report.html": "C:/Users/Jane/.ao/data/artifacts/ao-1/report.html",
		"/mnt/d/work/report.html":                           "D:/work/report.html",
		"/cygdrive/c/Users/Jane/report.html":                "C:/Users/Jane/report.html",
		`\c\Users\Jane\report.html`:                         "C:/Users/Jane/report.html",
		"/c":                                                "C:/",
		"C:/Users/Jane/report.html":                         "C:/Users/Jane/report.html",
		"report.html":                                       "report.html",
		"/mnt":                                              "/mnt",
		"/mnt/data/report.html":                             "/mnt/data/report.html",
		"/usr/report.html":                                  "/usr/report.html",
		`\\server\share\report.html`:                        `\\server\share\report.html`,
		"/Users/jane/.ao/data/artifacts/ao-1/subdir/report.html": "/Users/jane/.ao/data/artifacts/ao-1/subdir/report.html",
	} {
		if got := WindowsDrivePath(in); got != want {
			t.Errorf("WindowsDrivePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFromWorkingDir(t *testing.T) {
	dir := t.TempDir()
	writeArtifact(t, dir, "screens/index.html")
	writeArtifact(t, dir, "screens/shot.png")
	writeArtifact(t, dir, "brand.html")
	worktree := t.TempDir()
	writeArtifact(t, worktree, "brand.html")
	screens := filepath.Join(dir, "screens")

	for _, tc := range []struct {
		name, workingDir, reference, want string
	}{
		{"file in the subfolder the shell is in", screens, "index.html", filepath.Join(screens, "index.html")},
		{"parent of the subfolder", screens, "../brand.html", filepath.Join(dir, "brand.html")},
		{"path from the artifact directory's top, run in a subfolder", screens, "screens/shot.png", "screens/shot.png"},
		{"shell at the artifact directory", dir, "brand.html", filepath.Join(dir, "brand.html")},
		{"shell outside, a file of the same name there", worktree, "brand.html", "brand.html"},
		{"absolute path", screens, filepath.Join(dir, "brand.html"), filepath.Join(dir, "brand.html")},
		{"no working directory", "", "index.html", "index.html"},
		{"relative working directory", "screens", "index.html", "index.html"},
		{"missing file", screens, "gone.html", "gone.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromWorkingDir(dir, tc.workingDir, tc.reference); got != tc.want {
				t.Fatalf("FromWorkingDir(%q, %q) = %q, want %q", tc.workingDir, tc.reference, got, tc.want)
			}
		})
	}

	// The ao-105 case: run in a subfolder with paths relative to it.
	got, err := Registrable(dir, FromWorkingDir(dir, screens, "index.html"))
	if err != nil || got.Path != "screens/index.html" {
		t.Fatalf("Registrable = %q, %v", got.Path, err)
	}
}

func TestSplitAtSpace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Jane Doe", ".ao", "data", "artifacts", "ao-1")
	cut := dir[:strings.Index(dir, " Doe")]
	if !SplitAtSpace(dir, cut) {
		t.Fatalf("SplitAtSpace(%q, %q) = false", dir, cut)
	}
	if !SplitAtSpace(dir, strings.ToUpper(cut[:1])+cut[1:]) {
		t.Fatal("letter case defeated the check")
	}
	for _, reference := range []string{"report.html", filepath.Join(dir, "report.html"), filepath.Dir(cut), "/"} {
		if SplitAtSpace(dir, reference) {
			t.Errorf("SplitAtSpace(%q, %q) = true", dir, reference)
		}
	}
}
