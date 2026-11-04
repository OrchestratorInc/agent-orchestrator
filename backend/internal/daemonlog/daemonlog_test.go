package daemonlog

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDirSitsNextToTheRunFile(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "u")
	cases := map[string]string{
		filepath.Join(home, ".ao", "running.json"):        filepath.Join(home, ".ao", "logs"),
		filepath.Join(home, ".ao", "dev", "running.json"): filepath.Join(home, ".ao", "dev", "logs"),
	}
	for runFile, want := range cases {
		if got := Dir(runFile); got != want {
			t.Errorf("Dir(%q) = %q, want %q", runFile, got, want)
		}
	}
}

func TestOpenCreatesAnOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", FileName)
	l, err := Open(path, 1024, 2)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestOpenNarrowsAnExistingWideFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path, 1024, 2)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestOpenRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if l, err := Open(path, 1024, 2); err == nil {
		_ = l.Close()
		t.Fatal("Open followed a symlink")
	}
}

// Records rotate whole into numbered backups, the oldest beyond the cap is
// dropped, and no file outgrows the cap.
func TestWriteRotatesAndKeepsTheConfiguredBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	l, err := Open(path, 20, 2)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	for _, record := range []string{"record-1 aaaaa\n", "record-2 bbbbb\n", "record-3 ccccc\n", "record-4 ddddd\n"} {
		if _, err := l.Write([]byte(record)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	want := map[string]string{
		path:        "record-4 ddddd\n",
		path + ".1": "record-3 ccccc\n",
		path + ".2": "record-2 bbbbb\n",
	}
	for file, content := range want {
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if string(got) != content {
			t.Errorf("%s = %q, want %q", filepath.Base(file), got, content)
		}
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("daemon.log.3 should not exist: %v", err)
	}
}

// A record bigger than the cap still lands; it just starts a fresh file.
func TestWriteKeepsAnOversizedRecordWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	l, err := Open(path, 8, 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	big := strings.Repeat("x", 32) + "\n"
	if _, err := l.Write([]byte(big)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != big {
		t.Fatalf("daemon.log = %q, want the whole record", got)
	}
}

// When another process rotates the shared path (a predecessor daemon during a
// takeover), the next write follows the path to the new file.
func TestWriteFollowsAPathRotatedByAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	l, err := Open(path, 1024, 2)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot rename a file this process holds open")
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "after\n" {
		t.Fatalf("daemon.log = %q, want the record written after the external rotation", got)
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), FileName), 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("late\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write after Close = %v, want os.ErrClosed", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// A dead stderr (supervisor gone) must not stop the file write; the file gets
// the redacted record while the mirror keeps the original.
func TestTeeWritesTheRedactedRecordToTheFileWhateverTheMirrorDoes(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	l, err := Open(path, 1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	record := "level=INFO msg=auth token=s3cr3t-value\n"
	if n, err := Tee(l, failingWriter{}).Write([]byte(record)); err != nil || n != len(record) {
		t.Fatalf("Tee.Write = %d, %v; want %d, nil", n, err, len(record))
	}
	var mirror strings.Builder
	if _, err := Tee(l, &mirror).Write([]byte(record)); err != nil {
		t.Fatal(err)
	}
	if mirror.String() != record {
		t.Fatalf("mirror = %q, want the unredacted record", mirror.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "s3cr3t-value") || strings.Count(string(got), "token=[redacted]") != 2 {
		t.Fatalf("daemon.log = %q, want both records with the token redacted", got)
	}
}
