//go:build windows

package runfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestReaderDoesNotBlockDaemonRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 4242, Port: 3001}); err != nil {
		t.Fatal(err)
	}
	reader, err := openRead(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if err := Remove(path); err != nil {
		t.Fatalf("daemon removal while a reader holds the handshake: %v", err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("existing reader lost its file after removal: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(path); err != nil || got != nil {
		t.Fatalf("Read after daemon removal = (%+v, %v), want missing", got, err)
	}
}

func TestReadDoesNotTreatExclusiveAccessAsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 4242, Port: 3001}); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(locked) })
	got, err := Read(path)
	if got != nil || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("Read under an exclusive lock = (%+v, %v), want a sharing error, not missing", got, err)
	}
}

func TestReadDoesNotTreatAccessDeniedAsMissing(t *testing.T) {
	got, err := Read(t.TempDir())
	if got != nil || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("Read on an inaccessible directory = (%+v, %v), want access denied, not missing", got, err)
	}
}

func TestReadRetriesDeletePendingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 4242, Port: 3001}); err != nil {
		t.Fatal(err)
	}
	reader, err := openRead(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if err := Remove(path); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		closed <- reader.Close()
	}()
	got, err := Read(path)
	if closeErr := <-closed; closeErr != nil {
		t.Fatal(closeErr)
	}
	if got != nil || err != nil {
		t.Fatalf("Read after pending deletion finishes = (%+v, %v), want missing", got, err)
	}
}

func TestReadPreservesLongPathSupport(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < windows.MAX_PATH {
		dir = filepath.Join(dir, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "running.json")
	if err := os.WriteFile(path, []byte(`{"pid":4242,"port":3001}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil || got == nil || got.PID != 4242 {
		t.Fatalf("Read beyond MAX_PATH = (%+v, %v), want daemon 4242", got, err)
	}
}

func TestReadWhileDaemonHoldsDeleteHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 4242, Port: 3001}); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	deleting, err := windows.CreateFile(name, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(deleting) })
	got, err := Read(path)
	if err != nil || got == nil || got.PID != 4242 {
		t.Fatalf("Read with a concurrent deletion handle = (%+v, %v), want daemon 4242", got, err)
	}
}
