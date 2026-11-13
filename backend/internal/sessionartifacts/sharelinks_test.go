package sessionartifacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const shareLinksFixture = `{
	"[\"https://cp.test\",\"sess-1\",\"report.html\"]": {"shareId": "a", "fingerprint": "f", "url": "https://share.test/a"},
	"[\"https://cp.test\",\"sess-1\",\"charts/q3.html\"]": {"shareId": "q", "url": "https://share.test/q"},
	"[\"https://cp.test\",\"sess-1\",\"old.html\"]": "b",
	"[\"https://cp.test\",\"sess-1\",\"bad.html\"]": {"shareId": "c", "url": "javascript:alert(1)"},
	"[\"https://other.test\",\"sess-1\",\"notes.md\"]": {"shareId": "d", "url": "https://other.test/d"},
	"[\"https://cp.test\",\"sess-2\",\"notes.md\"]": {"shareId": "e", "url": "https://share.test/e"}
}`

func applied(links *ShareLinks, id domain.SessionID, paths ...string) map[string]string {
	files := make([]domain.SessionArtifactFile, len(paths))
	for i, path := range paths {
		files[i].Path = path
	}
	links.Apply(id, files)
	out := map[string]string{}
	for _, file := range files {
		if file.ShareURL != "" {
			out[file.Path] = file.ShareURL
		}
	}
	return out
}

func TestShareLinksApply(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ShareLinksFile)
	if err := os.WriteFile(file, []byte(shareLinksFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	links := NewShareLinks(dir, "https://cp.test")

	got := applied(links, "sess-1", "report.html", "charts/q3.html", "old.html", "bad.html", "notes.md")
	if len(got) != 2 || got["report.html"] != "https://share.test/a" || got["charts/q3.html"] != "https://share.test/q" {
		t.Fatalf("sess-1 links = %v, want report.html and charts/q3.html", got)
	}

	if err := os.WriteFile(file, []byte(`{"[\"https://cp.test\",\"sess-1\",\"report.html\"]": {"shareId": "a", "fingerprint": "f"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	if got := applied(links, "sess-1", "report.html"); len(got) != 0 {
		t.Fatalf("after unpublish links = %v, want none", got)
	}

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if got := applied(links, "sess-1", "report.html"); len(got) != 0 {
		t.Fatalf("without the file links = %v, want none", got)
	}
	var none *ShareLinks
	if got := applied(none, "sess-1", "report.html"); len(got) != 0 {
		t.Fatalf("nil ShareLinks set %v", got)
	}
}

// A record kept by artifact ID follows the file to its new path.
func TestShareLinksApplyByArtifactID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ShareLinksFile), []byte(`{
		"[\"https://cp.test\",\"sess-1\",\"\",\"art-1\"]": {"shareId": "a", "url": "https://share.test/a"},
		"[\"https://cp.test\",\"sess-1\",\"old.html\"]": {"shareId": "b", "url": "https://share.test/b"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []domain.SessionArtifactFile{
		{ID: "art-1", Path: "final/renamed.html"},
		{ID: "art-2", Path: "old.html"},
		{Path: "untracked.html"},
	}
	NewShareLinks(dir, "https://cp.test").Apply("sess-1", files)
	if files[0].ShareURL != "https://share.test/a" || files[1].ShareURL != "https://share.test/b" || files[2].ShareURL != "" {
		t.Fatalf("share URLs = %q, %q, %q", files[0].ShareURL, files[1].ShareURL, files[2].ShareURL)
	}
}
