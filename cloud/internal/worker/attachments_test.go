package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/attachments"
	"github.com/google/uuid"
)

type imageReader struct {
	url   string
	calls int
}

func (r *imageReader) AttachmentReadGrant(context.Context, string) (attachments.ReadGrant, error) {
	r.calls++
	return attachments.ReadGrant{URL: r.url}, nil
}
func TestMaterializeImagesDownloadsVerifiesExcludesAndRestores(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	data := b.Bytes()
	sum := sha256.Sum256(data)
	m := attachments.Metadata{ID: uuid.NewString(), Filename: "../../user name.png", Size: int64(len(data)), MIMEType: "image/png", SHA256: hex.EncodeToString(sum[:]), Status: "ready"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credential leaked to storage")
		}
		w.Write(data)
	}))
	defer server.Close()
	reader := &imageReader{url: server.URL + "/read"}
	makeWorkspace := func() string {
		t.Helper()
		dir := t.TempDir()
		if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %s %v", out, err)
		}
		return dir
	}
	workspace := makeWorkspace()
	ctx := context.Background()
	paths, err := MaterializeAttachments(ctx, workspace, server.URL, reader, []attachments.Metadata{m})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != AttachmentPath(m) {
		t.Fatal(paths)
	}
	got, err := os.ReadFile(filepath.Join(workspace, paths[0]))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("image not materialized", err)
	}
	git := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	git.Dir = workspace
	out, err := git.Output()
	if err != nil || bytes.Contains(out, []byte("image-")) {
		t.Fatal("image leaked into git", string(out), err)
	}
	if _, err := MaterializeAttachments(ctx, workspace, server.URL, reader, []attachments.Metadata{m}); err != nil || reader.calls != 1 {
		t.Fatal("cache retry", err, reader.calls)
	}
	if _, err := MaterializeAttachments(ctx, makeWorkspace(), server.URL, reader, []attachments.Metadata{m}); err != nil || reader.calls != 2 {
		t.Fatal("replacement restore", err, reader.calls)
	}
	// A wrong checksum cannot replace an existing verified image.
	bad := m
	bad.SHA256 = strings.Repeat("0", 64)
	if _, err := MaterializeAttachments(ctx, workspace, server.URL, reader, []attachments.Metadata{bad}); err == nil {
		t.Fatal("accepted checksum mismatch")
	}
	got, _ = os.ReadFile(filepath.Join(workspace, paths[0]))
	if !bytes.Equal(got, data) {
		t.Fatal("failed download replaced good bytes")
	}
	escaped := makeWorkspace()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(escaped, ".ao")); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeAttachments(ctx, escaped, server.URL, reader, []attachments.Metadata{m}); err == nil {
		t.Fatal("accepted attachment symlink")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside workspace")
	}
	m.ID = "../../escape"
	if _, err := MaterializeAttachments(ctx, workspace, server.URL, reader, []attachments.Metadata{m}); err == nil {
		t.Fatal("accepted invalid ID")
	}
}
func TestMaterializeRejectsExpiredGrant(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	sum := sha256.Sum256(b.Bytes())
	m := attachments.Metadata{ID: uuid.NewString(), Filename: "image.png", Size: int64(b.Len()), MIMEType: "image/png", SHA256: hex.EncodeToString(sum[:])}
	workspace := t.TempDir()
	exec.Command("git", "init", workspace).Run()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	if _, err := MaterializeAttachments(context.Background(), workspace, server.URL, &imageReader{url: server.URL}, []attachments.Metadata{m}); err == nil {
		t.Fatal("accepted expired grant")
	}
	entries, _ := os.ReadDir(filepath.Join(workspace, ".ao", "attachments"))
	if len(entries) != 0 {
		t.Fatal("partial file left behind")
	}
}
