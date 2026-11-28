package sessionmanager

import (
	"archive/tar"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeSized(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err == nil {
		err = f.Truncate(size)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestPackTransferLeavesOutAConversationThatDoesNotFit(t *testing.T) {
	dir := t.TempDir()
	writeSized(t, filepath.Join(dir, transferBundleName), 10)
	writeSized(t, filepath.Join(dir, transferConversationName), MaxTransferArchiveBytes)
	archive, withConversation, err := packTransfer(dir)
	if err != nil || archive == nil || withConversation {
		t.Fatalf("pack = %+v, %v, %v; want the bundle alone", archive, withConversation, err)
	}
	out := t.TempDir()
	if err := unpackTransfer(filepath.Join(dir, transferArchiveName), out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, transferBundleName)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, transferConversationName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conversation unpacked: %v", err)
	}

	writeSized(t, filepath.Join(dir, transferBundleName), MaxTransferArchiveBytes)
	if _, _, err := packTransfer(dir); !errors.Is(err, ErrTransferTooLarge) {
		t.Fatalf("an oversized bundle = %v, want ErrTransferTooLarge", err)
	}
}

func TestUnpackTransferRefusesOtherEntries(t *testing.T) {
	for _, name := range []string{"../git.bundle", "payload.json"} {
		archive := filepath.Join(t.TempDir(), transferArchiveName)
		f, err := os.Create(archive)
		if err != nil {
			t.Fatal(err)
		}
		tw := tar.NewWriter(f)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte("x"))
		_ = tw.Close()
		_ = f.Close()
		if err := unpackTransfer(archive, t.TempDir()); !errors.Is(err, ErrTransferArchive) {
			t.Fatalf("entry %q = %v, want ErrTransferArchive", name, err)
		}
	}
}
