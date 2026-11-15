package runfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "running.json")
	want := Info{
		PID: 4242, Port: 3001,
		StartedAt: time.Now().UTC().Truncate(time.Second),
		AppRunID:  "apprun-1",
	}

	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil {
		t.Fatal("Read returned nil for an existing file")
		return
	}
	if got.PID != want.PID || got.Port != want.Port || got.AppRunID != want.AppRunID || !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("round trip mismatch: got %+v, want %+v", *got, want)
	}
}

func TestWriteReadRoundTripOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")

	want := Info{PID: 1, Port: 3001, Owner: "app"}
	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil {
		t.Fatal("Read returned nil for an existing file")
		return
	}
	if got.Owner != "app" {
		t.Errorf("Owner round trip: got %q, want %q", got.Owner, "app")
	}

	headless := Info{PID: 2, Port: 3002}
	if err := Write(path, headless); err != nil {
		t.Fatalf("Write headless: %v", err)
	}
	got, err = Read(path)
	if err != nil {
		t.Fatalf("Read headless: %v", err)
	}
	if got == nil {
		t.Fatal("Read returned nil for headless file")
		return
	}
	if got.Owner != "" {
		t.Errorf("headless Owner round trip: got %q, want %q", got.Owner, "")
	}
}

func TestWriteOverwritesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")

	if err := Write(path, Info{PID: 1, Port: 3001}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := Write(path, Info{PID: 2, Port: 3002}); err != nil {
		t.Fatalf("second Write (overwrite): %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil || got.PID != 2 || got.Port != 3002 {
		t.Errorf("after overwrite: got %+v, want PID=2 Port=3002", got)
	}
}

func TestReadMissingIsNotError(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if got != nil {
		t.Errorf("Read missing = %+v, want nil", got)
	}
}

func TestRemoveIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Remove(path); err != nil {
		t.Errorf("Remove on missing file: %v", err)
	}
	if err := Write(path, Info{PID: 1, Port: 2}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Remove(path); err != nil {
		t.Errorf("Remove existing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still present after Remove")
	}
}

func TestRemoveIfOwnedDoesNotDeleteSuccessorRunfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 1, Port: 3001}); err != nil {
		t.Fatalf("Write predecessor: %v", err)
	}
	if err := Write(path, Info{PID: 2, Port: 3002}); err != nil {
		t.Fatalf("Write successor: %v", err)
	}
	if err := RemoveIfOwned(path, 1); err != nil {
		t.Fatalf("RemoveIfOwned predecessor: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil || got.PID != 2 || got.Port != 3002 {
		t.Fatalf("successor runfile was removed or changed: %+v", got)
	}
	if err := RemoveIfOwned(path, 2); err != nil {
		t.Fatalf("RemoveIfOwned successor: %v", err)
	}
	if got, err := Read(path); err != nil || got != nil {
		t.Fatalf("after owner removal got=%+v err=%v", got, err)
	}
}

func TestCheckStaleDeadPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: 0x7FFFFFFF, Port: 3001}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	live, err := CheckStale(path)
	if err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if live != nil {
		t.Errorf("CheckStale on dead PID = %+v, want nil (stale, safe to overwrite)", live)
	}
}

func TestCheckStaleLivePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Write(path, Info{PID: os.Getpid(), Port: 3001}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	live, err := CheckStale(path)
	if err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if live == nil {
		t.Fatal("CheckStale on live PID = nil, want the live Info")
		return
	}
	if live.PID != os.Getpid() {
		t.Errorf("live.PID = %d, want %d", live.PID, os.Getpid())
	}
}

func TestCheckStaleNoFile(t *testing.T) {
	live, err := CheckStale(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if live != nil {
		t.Errorf("CheckStale with no file = %+v, want nil", live)
	}
}
