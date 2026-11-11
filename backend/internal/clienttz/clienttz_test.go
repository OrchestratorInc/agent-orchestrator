package clienttz

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecorderPersistsLatestValidZone(t *testing.T) {
	dir := t.TempDir()
	r := NewRecorder(dir)
	if got := Read(dir); got != "" {
		t.Fatalf("fresh dir Read = %q, want empty", got)
	}

	r.Observe("Asia/Kolkata")
	if got := Read(dir); got != "Asia/Kolkata" {
		t.Fatalf("Read = %q, want Asia/Kolkata", got)
	}

	for _, bad := range []string{"", "Local", "Not/AZone", "/etc/passwd", "../../etc/passwd", "Asia/../UTC"} {
		r.Observe(bad)
		if got := Read(dir); got != "Asia/Kolkata" {
			t.Fatalf("after Observe(%q) Read = %q, want Asia/Kolkata kept", bad, got)
		}
	}

	r.Observe(" America/Los_Angeles ")
	if got := Read(dir); got != "America/Los_Angeles" {
		t.Fatalf("Read = %q, want America/Los_Angeles", got)
	}

	// A client really set to UTC is the user's own setting.
	r.Observe("UTC")
	if got := Read(dir); got != "UTC" {
		t.Fatalf("Read = %q, want UTC", got)
	}

	// A new recorder (daemon restart) starts from the persisted value.
	if got := NewRecorder(dir).last; got != "UTC" {
		t.Fatalf("restarted recorder last = %q, want UTC", got)
	}
}

func TestReadIgnoresCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("garbage zone"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Read(dir); got != "" {
		t.Fatalf("Read = %q, want empty for corrupt file", got)
	}
}

func TestNilAndEmptyRecorderIgnoreReports(t *testing.T) {
	var r *Recorder
	r.Observe("Asia/Kolkata")
	NewRecorder("").Observe("Asia/Kolkata")
	if got := Read(""); got != "" {
		t.Fatalf("Read(\"\") = %q", got)
	}
}
