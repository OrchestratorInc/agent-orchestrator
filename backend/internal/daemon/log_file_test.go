package daemon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// The desktop app keeps only a small in-memory copy of the daemon's stderr, so
// a field failure was undiagnosable after a restart (#6200). The daemon's own
// log must land in its data dir and survive the process.
func TestNewLoggerPersistsToDataDir(t *testing.T) {
	dataDir := t.TempDir()
	newLogger(dataDir).Error("reconcile: live pass failed, skipping", "sessionID", "mer-1")

	got := readLog(t, filepath.Join(dataDir, daemonLogName))
	if !strings.Contains(got, "reconcile: live pass failed, skipping") || !strings.Contains(got, "sessionID=mer-1") {
		t.Fatalf("daemon.log = %q, want the logged record", got)
	}
}

func TestRotatingLogFileAppendsAcrossOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), daemonLogName)
	for _, line := range []string{"first boot\n", "second boot\n"} {
		file, err := openRotatingLogFile(path, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
		_ = file.file.Close()
	}
	if got := readLog(t, path); got != "first boot\nsecond boot\n" {
		t.Fatalf("daemon.log = %q, want both boots appended", got)
	}
}

func TestRotatingLogFileKeepsOnePreviousFileWithinCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), daemonLogName)
	file, err := openRotatingLogFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"aaaaaaaa\n", "bbbbbbbb\n", "cccccccc\n"} {
		if _, err := file.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if got := readLog(t, path); got != "cccccccc\n" {
		t.Fatalf("daemon.log = %q, want only the newest record", got)
	}
	if got := readLog(t, path+".1"); got != "bbbbbbbb\n" {
		t.Fatalf("daemon.log.1 = %q, want the record before the last rotation", got)
	}
	if _, err := os.Stat(path + ".2"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("daemon.log.2 exists (err=%v); rotation must keep a single previous file", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestTeeLogWriterKeepsEachCopyWhenTheOtherFails(t *testing.T) {
	var file bytes.Buffer
	if _, err := (teeLogWriter{stderr: failingWriter{}, file: &file}).Write([]byte("kept\n")); err != nil {
		t.Fatalf("dead stderr failed the write: %v", err)
	}
	if file.String() != "kept\n" {
		t.Fatalf("file copy = %q, want it written despite a dead stderr", file.String())
	}

	var stderr bytes.Buffer
	if _, err := (teeLogWriter{stderr: &stderr, file: failingWriter{}}).Write([]byte("kept\n")); err != nil {
		t.Fatalf("failing file failed the write: %v", err)
	}
	if stderr.String() != "kept\n" {
		t.Fatalf("stderr copy = %q, want it written despite a failing file", stderr.String())
	}
}
