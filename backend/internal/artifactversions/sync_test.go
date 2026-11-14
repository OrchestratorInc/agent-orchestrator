package artifactversions

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSnapshotRecordsAnEditStillInsideTheSettleWindow(t *testing.T) {
	h := newHarness(t)
	h.write("plan.md", "first")
	h.tick()
	h.tick()
	h.write("plan.md", "second")
	h.cap.now = func() time.Time { return h.mtime.Add(SettleWindow / 2) }

	sum, err := h.cap.Snapshot(h.ctx, h.sess, "plan.md")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	vs := h.versions("plan.md")
	if len(vs) != 2 || vs[0].Source != domain.ArtifactVersionSourceAgent {
		t.Fatalf("versions = %+v, want the fresh edit kept as a second, agent-written one", vs)
	}
	if want := sha256Hex("second"); sum != want || vs[0].SHA256 != want {
		t.Fatalf("Snapshot sum = %q, latest = %q, want the kept content's %q", sum, vs[0].SHA256, want)
	}

	// Content already kept is not recorded again, and its sum is still returned.
	if sum, err := h.cap.Snapshot(h.ctx, h.sess, "plan.md"); err != nil || sum != sha256Hex("second") {
		t.Fatalf("second Snapshot = %q, %v, want the latest version's sum", sum, err)
	}
	if got := len(h.versions("plan.md")); got != 2 {
		t.Fatalf("versions after a repeat Snapshot = %d, want 2", got)
	}
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestSyncRecordsASettledEditWithoutWaitingForTheObserver(t *testing.T) {
	h := newHarness(t)
	h.write("plan.md", "first")
	h.tick()
	h.tick()
	h.write("plan.md", "second")

	// The file was just written, so it may still be in flux.
	h.cap.now = func() time.Time { return h.mtime.Add(SettleWindow / 2) }
	if err := h.cap.Sync(h.ctx, h.sess, "plan.md"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := len(h.versions("plan.md")); got != 1 {
		t.Fatalf("versions while unsettled = %d, want 1", got)
	}

	h.cap.now = func() time.Time { return h.mtime.Add(2 * SettleWindow) }
	if err := h.cap.Sync(h.ctx, h.sess, "plan.md"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	vs := h.versions("plan.md")
	if len(vs) != 2 || vs[0].Source != domain.ArtifactVersionSourceAgent {
		t.Fatalf("versions after settling = %+v, want a second, agent-written one", vs)
	}

	// Syncing again, or an observer tick, adds nothing.
	if err := h.cap.Sync(h.ctx, h.sess, "plan.md"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	h.tick()
	h.tick()
	if got := len(h.versions("plan.md")); got != 2 {
		t.Fatalf("versions after repeats = %d, want 2", got)
	}
}

// A turn start keeps every artifact as it is then, with no settle window, so
// a fork of that turn can copy the artifacts as they were.
func TestSnapshotSessionKeepsEveryFileNow(t *testing.T) {
	h := newHarness(t)
	h.write("report.md", "turn one")
	h.write("notes/todo.md", "a list")
	h.cap.now = func() time.Time { return h.mtime } // both still inside the settle window
	if err := h.cap.SnapshotSession(h.ctx, h.sess); err != nil {
		t.Fatal(err)
	}
	if vs := h.versions("report.md"); len(vs) != 1 || vs[0].SHA256 != sha256Hex("turn one") {
		t.Fatalf("report.md versions = %+v", vs)
	}
	if vs := h.versions("notes/todo.md"); len(vs) != 1 {
		t.Fatalf("notes/todo.md versions = %+v", vs)
	}
	// Unchanged content is not recorded twice.
	if err := h.cap.SnapshotSession(h.ctx, h.sess); err != nil {
		t.Fatal(err)
	}
	if vs := h.versions("report.md"); len(vs) != 1 {
		t.Fatalf("report.md versions after a second call = %d", len(vs))
	}
}
