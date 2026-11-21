package artifactversions

import (
	"os"
	"path/filepath"
	"testing"
)

func (h *harness) dataDir() string {
	return filepath.Dir(filepath.Dir(h.dir))
}

func TestDeleteSessionRemovesFilesAndVersionsAndKeepsAnEmptyDir(t *testing.T) {
	h := newHarness(t)
	h.write("report.html", "<h1>v1</h1>")
	h.tick()
	h.tick()
	if len(h.versions("report.html")) != 1 || h.blobCount() != 1 {
		t.Fatalf("setup: versions=%d blobs=%d, want one captured version", len(h.versions("report.html")), h.blobCount())
	}

	if err := h.cap.DeleteSession(h.ctx, h.sess.ID); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(h.dir)
	if err != nil {
		t.Fatalf("artifact dir should remain for a restore: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("artifact dir entries = %v, want empty", entries)
	}
	if vs := h.versions("report.html"); len(vs) != 0 {
		t.Fatalf("versions after delete = %+v, want none", vs)
	}
	if trash, _ := os.ReadDir(filepath.Join(h.dataDir(), trashDir)); len(trash) != 0 {
		t.Fatalf("trash entries = %v, want the moved dir removed", trash)
	}
	// A capture queued behind the delete finds nothing to record.
	h.tick()
	if vs := h.versions("report.html"); len(vs) != 0 {
		t.Fatalf("versions after a later capture = %+v, want none", vs)
	}
}

func TestDeleteSessionLeavesEverythingWhenTheMoveFails(t *testing.T) {
	h := newHarness(t)
	h.write("report.html", "<h1>v1</h1>")
	h.tick()
	h.tick()
	// A file where the trash directory belongs makes the move fail up front.
	if err := os.WriteFile(filepath.Join(h.dataDir(), trashDir), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.cap.DeleteSession(h.ctx, h.sess.ID); err == nil {
		t.Fatal("DeleteSession succeeded, want an error")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "report.html")); err != nil {
		t.Fatalf("artifact removed by a failed delete: %v", err)
	}
	if vs := h.versions("report.html"); len(vs) != 1 {
		t.Fatalf("versions after a failed delete = %d, want 1", len(vs))
	}
}

func TestDeleteSessionEmptiesLeftoverTrash(t *testing.T) {
	h := newHarness(t)
	leftover := filepath.Join(h.dataDir(), trashDir, "old-1", "page.html")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.cap.DeleteSession(h.ctx, h.sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(leftover)); !os.IsNotExist(err) {
		t.Fatalf("leftover trash still present: %v", err)
	}
}

func TestDeleteVersionsRemovesOnePathAndKeepsSharedBlobs(t *testing.T) {
	h := newHarness(t)
	h.write("report.html", "<h1>v1</h1>")
	h.write("copy.html", "<h1>v1</h1>")
	h.tick()
	h.tick()
	h.write("report.html", "<h1>v2</h1>")
	h.tick()
	h.tick()
	if len(h.versions("report.html")) != 2 || h.blobCount() != 2 {
		t.Fatalf("setup: versions=%d blobs=%d, want 2 and 2", len(h.versions("report.html")), h.blobCount())
	}
	shared := h.versions("copy.html")[0].SHA256
	own := h.versions("report.html")[0].SHA256

	if err := os.Remove(filepath.Join(h.dir, "report.html")); err != nil {
		t.Fatal(err)
	}
	if err := h.cap.DeleteVersions(h.ctx, h.sess, "report.html"); err != nil {
		t.Fatal(err)
	}

	if vs := h.versions("report.html"); len(vs) != 0 {
		t.Fatalf("versions after delete = %+v, want none", vs)
	}
	if _, ok := h.blob(own); ok {
		t.Fatal("blob only report.html used is still stored")
	}
	if _, ok := h.blob(shared); !ok {
		t.Fatal("blob copy.html still uses was removed")
	}
	if vs := h.versions("copy.html"); len(vs) != 1 {
		t.Fatalf("copy.html versions = %d, want 1", len(vs))
	}
	h.tick()
	h.tick()
	if vs := h.versions("report.html"); len(vs) != 0 {
		t.Fatalf("versions after a later capture = %+v, want none", vs)
	}
}
