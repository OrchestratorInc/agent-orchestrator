package artifactversions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type harness struct {
	t     *testing.T
	ctx   context.Context
	store *sqlite.Store
	sess  domain.SessionRecord
	dir   string
	cap   *Capturer
	mtime time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, filepath.Join(dataDir, "db"))
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "ao", Path: dataDir, RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	sess, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "ao", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := sessionartifacts.Dir(dataDir, sess.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &harness{
		t: t, ctx: ctx, store: store, sess: sess, dir: dir,
		cap:   New(store, dataDir, nil),
		mtime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// write sets a file's content and gives it a fresh modification time, so
// every write is observed as a change regardless of filesystem resolution.
func (h *harness) write(rel, body string) {
	h.t.Helper()
	p := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.touch(rel)
}

func (h *harness) touch(rel string) {
	h.t.Helper()
	h.mtime = h.mtime.Add(time.Second)
	if err := os.Chtimes(filepath.Join(h.dir, filepath.FromSlash(rel)), h.mtime, h.mtime); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) tick() {
	h.t.Helper()
	if err := h.cap.CaptureSession(h.ctx, h.sess); err != nil {
		h.t.Fatalf("CaptureSession: %v", err)
	}
}

func (h *harness) versions(rel string) []domain.SessionArtifactVersion {
	h.t.Helper()
	vs, err := h.store.ListSessionArtifactVersions(h.ctx, h.sess.ID, rel)
	if err != nil {
		h.t.Fatal(err)
	}
	return vs
}

func (h *harness) blob(sum string) (string, bool) {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, blobPath(sum)))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return string(data), true
}

func (h *harness) blobCount() int {
	h.t.Helper()
	n := 0
	root := filepath.Join(h.dir, blobsDir)
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		h.t.Fatal(err)
	}
	return n
}

func sum(body string) string {
	s := sha256.Sum256([]byte(body))
	return hex.EncodeToString(s[:])
}

func TestCaptureSession_WaitsForFileToSettle(t *testing.T) {
	h := newHarness(t)
	h.write("page.html", "<p>one</p>")
	h.tick()
	if vs := h.versions("page.html"); len(vs) != 0 {
		t.Fatalf("captured on first sighting: %+v", vs)
	}
	h.write("page.html", "<p>one, still writing</p>")
	h.tick()
	if vs := h.versions("page.html"); len(vs) != 0 {
		t.Fatalf("captured a file that changed since the last call: %+v", vs)
	}
	h.tick()
	vs := h.versions("page.html")
	if len(vs) != 1 || vs[0].Version != 1 || vs[0].SHA256 != sum("<p>one, still writing</p>") || vs[0].ContentType != "text/html; charset=utf-8" {
		t.Fatalf("versions = %+v", vs)
	}
	if body, ok := h.blob(vs[0].SHA256); !ok || body != "<p>one, still writing</p>" {
		t.Fatalf("blob = %q ok=%v", body, ok)
	}

	h.write("page.html", "<p>two</p>")
	h.tick()
	h.tick()
	vs = h.versions("page.html")
	if len(vs) != 2 || vs[0].Version != 2 || vs[0].SHA256 != sum("<p>two</p>") {
		t.Fatalf("versions after edit = %+v", vs)
	}
	h.tick()
	if vs := h.versions("page.html"); len(vs) != 2 {
		t.Fatalf("an unchanged file was captured again: %+v", vs)
	}
}

func TestCaptureSession_TouchWithoutContentChangeAddsNoVersion(t *testing.T) {
	h := newHarness(t)
	h.write("notes.md", "# same")
	h.tick()
	h.tick()
	h.touch("notes.md")
	h.tick()
	h.tick()
	if vs := h.versions("notes.md"); len(vs) != 1 {
		t.Fatalf("versions = %+v, want only the first", vs)
	}
	if obs := h.cap.observed[h.sess.ID]["notes.md"]; !obs.matched {
		t.Fatalf("observation = %+v, want it marked as matching the latest version", obs)
	}
}

func TestCaptureSession_RevertToEarlierContentIsANewVersionSharingTheBlob(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{"a", "b", "a"} {
		h.write("page.html", body)
		h.tick()
		h.tick()
	}
	vs := h.versions("page.html")
	if len(vs) != 3 || vs[0].SHA256 != vs[2].SHA256 {
		t.Fatalf("versions = %+v", vs)
	}
	if n := h.blobCount(); n != 2 {
		t.Fatalf("blobs = %d, want 2", n)
	}
}

func TestCaptureSession_IdenticalContentUnderTwoPathsSharesOneBlob(t *testing.T) {
	h := newHarness(t)
	h.write("a.html", "same")
	h.write("sub/b.html", "same")
	h.tick()
	h.tick()
	if len(h.versions("a.html")) != 1 || len(h.versions("sub/b.html")) != 1 {
		t.Fatalf("a=%+v b=%+v", h.versions("a.html"), h.versions("sub/b.html"))
	}
	if n := h.blobCount(); n != 1 {
		t.Fatalf("blobs = %d, want 1", n)
	}
}

func TestCaptureSession_PrunesOldestVersionsAndOnlyUnreferencedBlobs(t *testing.T) {
	h := newHarness(t)
	h.write("other.html", "v1")
	for i := 1; i <= MaxVersionsPerFile+2; i++ {
		body := fmt.Sprintf("v%02d", i)
		if i == 1 {
			body = "v1" // same content as other.html
		}
		h.write("page.html", body)
		h.tick()
		h.tick()
	}
	vs := h.versions("page.html")
	if len(vs) != MaxVersionsPerFile || vs[len(vs)-1].Version != 3 || vs[0].Version != MaxVersionsPerFile+2 {
		t.Fatalf("got %d versions, oldest %d newest %d", len(vs), vs[len(vs)-1].Version, vs[0].Version)
	}
	// v1 of page.html was pruned, but other.html still references its blob.
	if _, ok := h.blob(sum("v1")); !ok {
		t.Fatal("blob still referenced by other.html was deleted")
	}
	// v2 of page.html was pruned and nothing else references it.
	if _, ok := h.blob(sum("v02")); ok {
		t.Fatal("unreferenced blob of a pruned version was kept")
	}
	if n := h.blobCount(); n != MaxVersionsPerFile+1 {
		t.Fatalf("blobs = %d, want %d", n, MaxVersionsPerFile+1)
	}
}

func TestCaptureSession_SkipsOversizeFile(t *testing.T) {
	h := newHarness(t)
	p := filepath.Join(h.dir, "big.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	h.tick()
	h.tick()
	h.tick()
	if vs := h.versions("big.bin"); len(vs) != 0 {
		t.Fatalf("oversize file versioned: %+v", vs)
	}
	if n := h.blobCount(); n != 0 {
		t.Fatalf("blobs = %d, want 0", n)
	}
}

func TestCaptureSession_SweepsInterruptedWritesOnce(t *testing.T) {
	h := newHarness(t)
	leftover := filepath.Join(h.dir, blobsDir, incomingPrefix+"dead")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.write("page.html", "x")
	h.tick()
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("leftover still present: %v", err)
	}
}

func TestCaptureSession_SnapshotsAreNotArtifacts(t *testing.T) {
	h := newHarness(t)
	h.write("page.html", "x")
	h.tick()
	h.tick()
	files, err := sessionartifacts.List(context.Background(), h.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "page.html" {
		t.Fatalf("files = %+v", files)
	}
}

func TestCaptureSession_RefusesVersionsSymlinkOutsideArtifactDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs elevated privileges on Windows")
	}
	h := newHarness(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(h.dir, sessionartifacts.VersionsDir)); err != nil {
		t.Fatal(err)
	}
	h.write("page.html", "x")
	h.tick()
	h.tick()
	if vs := h.versions("page.html"); len(vs) != 0 {
		t.Fatalf("versioned through an escaping symlink: %+v", vs)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("wrote outside the artifact directory: %v", entries)
	}
}

func TestCaptureSession_BoundsBytesCopiedPerCall(t *testing.T) {
	h := newHarness(t)
	big := make([]byte, MaxBytesPerSession/2+1)
	for _, name := range []string{"a.bin", "b.bin"} {
		if err := os.WriteFile(filepath.Join(h.dir, name), big, 0o600); err != nil {
			t.Fatal(err)
		}
		h.touch(name)
	}
	h.tick()
	h.tick()
	if a, b := len(h.versions("a.bin")), len(h.versions("b.bin")); a != 1 || b != 0 {
		t.Fatalf("after one capture call a=%d b=%d, want 1 and 0", a, b)
	}
	h.tick()
	if b := len(h.versions("b.bin")); b != 1 {
		t.Fatalf("deferred file not captured on the next call: b=%d", b)
	}
}

func TestCaptureSession_NoArtifactDirIsNotAnError(t *testing.T) {
	h := newHarness(t)
	if err := os.RemoveAll(h.dir); err != nil {
		t.Fatal(err)
	}
	h.tick()
	if _, ok := h.cap.observed[h.sess.ID]; ok {
		t.Fatal("kept observations for a session with no artifacts")
	}
}
