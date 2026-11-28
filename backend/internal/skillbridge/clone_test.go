package skillbridge

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A clone either makes an identical file or fails leaving nothing, because the
// copy that follows a failed clone creates the file exclusively. Where clones
// are unsupported (most Linux file systems, Windows) this is the failure path.
func TestCloneFileCopiesOrLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	want := bytes.Repeat([]byte("skill "), 4096)
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cloneFile(src, dst); err != nil {
		if _, statErr := os.Lstat(dst); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("clone failed (%v) but left %s behind: %v", err, dst, statErr)
		}
		return
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("clone = %d bytes, %v; want %d identical bytes", len(got), err, len(want))
	}
}
