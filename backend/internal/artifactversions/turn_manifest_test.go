package artifactversions

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// A turn's artifacts stay readable however a later turn changes them. The
// version store moves a renamed file's history to its new path, so the old
// path has none, and keeps only a file's latest versions; a fork of the
// earlier turn copies the turn's artifacts from what the turn kept.
func TestSnapshotTurnKeepsArtifactsALaterTurnRenamesOrReplaces(t *testing.T) {
	h := newHarness(t)
	h.write("report.md", "q3 report")
	h.write("page.html", "<p>one</p>")
	h.write("notes.md", "notes")
	h.write("scratch.txt", "scratch")
	track := func() {
		t.Helper()
		rows, err := h.store.ListSessionArtifacts(h.ctx, h.sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.cap.Track(h.ctx, h.sess, rows); err != nil {
			t.Fatal(err)
		}
	}
	track()
	if err := h.cap.SnapshotTurn(h.ctx, h.sess, []string{"turn-b"}); err != nil {
		t.Fatal(err)
	}
	// The next turn renames one artifact, renames another over a third, and
	// deletes a fourth.
	for _, move := range [][2]string{{"page.html", "final.html"}, {"notes.md", "report.md"}} {
		if err := os.Rename(filepath.Join(h.dir, move[0]), filepath.Join(h.dir, move[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(h.dir, "scratch.txt")); err != nil {
		t.Fatal(err)
	}
	track()
	track()
	if vs := h.versions("page.html"); len(vs) != 0 {
		t.Fatalf("precondition: page.html's history didn't move with it: %+v", vs)
	}
	files, found, err := sessionartifacts.TurnArtifacts(h.dir, "turn-b")
	if err != nil || !found {
		t.Fatalf("turn artifacts = %v, %v", found, err)
	}
	for path, want := range map[string]string{"report.md": "q3 report", "page.html": "<p>one</p>", "notes.md": "notes", "scratch.txt": "scratch"} {
		got, err := os.ReadFile(sessionartifacts.KeptBlobPath(h.dir, files[path]))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
	if _, ok := files["final.html"]; ok {
		t.Error("a path first seen after the turn is in its list")
	}
	if _, found, _ := sessionartifacts.TurnArtifacts(h.dir, "turn-unknown"); found {
		t.Error("a turn with nothing kept reports a list")
	}
}

// Only the latest MaxKeptTurns turns keep their artifact lists, and content
// that no kept list names any more is removed, so a session that is never
// forked holds a bounded amount of it.
func TestSnapshotTurnKeepsOnlyTheLatestTurns(t *testing.T) {
	h := newHarness(t)
	clock := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	h.cap.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	h.write("notes.md", "unchanged notes")
	total := MaxKeptTurns + 2
	for i := 1; i <= total; i++ {
		h.write("report.md", fmt.Sprintf("report as turn %d started", i))
		if err := h.cap.SnapshotTurn(h.ctx, h.sess, []string{fmt.Sprintf("turn-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= total; i++ {
		_, found, err := sessionartifacts.TurnArtifacts(h.dir, fmt.Sprintf("turn-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if want := i > total-MaxKeptTurns; found != want {
			t.Errorf("turn-%d kept = %v, want %v", i, found, want)
		}
	}
	if _, err := os.Stat(sessionartifacts.KeptBlobPath(h.dir, sum("report as turn 1 started"))); !os.IsNotExist(err) {
		t.Errorf("content only a dropped turn named is still kept: %v", err)
	}
	for _, body := range []string{fmt.Sprintf("report as turn %d started", total), "unchanged notes"} {
		if _, err := os.Stat(sessionartifacts.KeptBlobPath(h.dir, sum(body))); err != nil {
			t.Errorf("content a kept turn names was removed: %q: %v", body, err)
		}
	}
}
