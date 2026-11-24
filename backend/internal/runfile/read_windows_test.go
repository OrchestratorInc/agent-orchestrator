//go:build windows

package runfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// DeleteFile holds a DELETE handle while the CLI polls running.json. A reader
// must share deletion or it fails with ERROR_SHARING_VIOLATION during shutdown.
func TestReadWhileRunFileIsBeingDeleted(t *testing.T) {
	for _, longPath := range []bool{false, true} {
		name := "short path"
		if longPath {
			name = "long path"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if longPath {
				for len(dir) < windows.MAX_PATH {
					dir = filepath.Join(dir, strings.Repeat("d", 40))
				}
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "running.json")
			if err := os.WriteFile(path, []byte(`{"pid":4242,"port":3001}`), 0o600); err != nil {
				t.Fatal(err)
			}
			pathp, err := windows.UTF16PtrFromString(`\\?\` + path)
			if err != nil {
				t.Fatal(err)
			}
			deleting, err := windows.CreateFile(pathp, windows.DELETE,
				windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
				nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = windows.CloseHandle(deleting) }()
			got, err := Read(path)
			if err != nil || got == nil || got.PID != 4242 || got.Port != 3001 {
				t.Fatalf("Read under a delete handle = (%+v, %v), want the run-file", got, err)
			}
		})
	}
}

func TestRunFileReaderAllowsRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 4242, Port: 3001}); err != nil {
		t.Fatal(err)
	}
	reader, err := openRunFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if err := Remove(path); err != nil {
		t.Fatalf("Remove under a run-file reader: %v", err)
	}
}

func TestReadWaitsForPendingDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 4242, Port: 3001}); err != nil {
		t.Fatal(err)
	}
	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	deleting, err := windows.CreateFile(pathp, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	deleteFile := byte(1)
	if err := windows.SetFileInformationByHandle(deleting, windows.FileDispositionInfo, &deleteFile, 1); err != nil {
		_ = windows.CloseHandle(deleting)
		t.Fatal(err)
	}
	// Windows refuses new readers until the final delete handle closes.
	closed := make(chan struct{})
	time.AfterFunc(30*time.Millisecond, func() {
		_ = windows.CloseHandle(deleting)
		close(closed)
	})
	defer func() { <-closed }()
	if got, err := Read(path); err != nil || got != nil {
		t.Fatalf("Read while deletion completes = (%+v, %v), want a missing run-file", got, err)
	}
}

func TestReadDoesNotHidePersistentSharingErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 4242, Port: 3001}); err != nil {
		t.Fatal(err)
	}
	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	exclusive, err := windows.CreateFile(pathp, windows.GENERIC_READ, 0,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(exclusive) }()
	if got, err := Read(path); got != nil || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("Read under an exclusive handle = (%+v, %v), want a sharing error", got, err)
	}
}
