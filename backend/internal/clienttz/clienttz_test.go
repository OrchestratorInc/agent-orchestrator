package clienttz

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRecorderPersistsLatestValidZone(t *testing.T) {
	dir := t.TempDir()
	r := NewRecorder(context.Background(), dir)
	if got := Read(context.Background(), dir); got != "" {
		t.Fatalf("fresh dir Read = %q, want empty", got)
	}

	r.Observe(context.Background(), "Asia/Kolkata")
	if got := Read(context.Background(), dir); got != "Asia/Kolkata" {
		t.Fatalf("Read = %q, want Asia/Kolkata", got)
	}

	for _, bad := range []string{"", "Local", "Not/AZone", "/etc/passwd", "../../etc/passwd", "Asia/../UTC"} {
		r.Observe(context.Background(), bad)
		if got := Read(context.Background(), dir); got != "Asia/Kolkata" {
			t.Fatalf("after Observe(%q) Read = %q, want Asia/Kolkata kept", bad, got)
		}
	}

	r.Observe(context.Background(), " America/Los_Angeles ")
	if got := Read(context.Background(), dir); got != "America/Los_Angeles" {
		t.Fatalf("Read = %q, want America/Los_Angeles", got)
	}

	// A client really set to UTC is the user's own setting.
	r.Observe(context.Background(), "UTC")
	if got := Read(context.Background(), dir); got != "UTC" {
		t.Fatalf("Read = %q, want UTC", got)
	}

	// A new recorder (daemon restart) starts from the persisted value.
	if got := NewRecorder(context.Background(), dir).last; got != "UTC" {
		t.Fatalf("restarted recorder last = %q, want UTC", got)
	}
}

func TestReadIgnoresCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("garbage zone"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Read(context.Background(), dir); got != "" {
		t.Fatalf("Read = %q, want empty for corrupt file", got)
	}
}

func TestNilAndEmptyRecorderIgnoreReports(t *testing.T) {
	var r *Recorder
	r.Observe(context.Background(), "Asia/Kolkata")
	NewRecorder(context.Background(), "").Observe(context.Background(), "Asia/Kolkata")
	if got := Read(context.Background(), ""); got != "" {
		t.Fatalf("Read(\"\") = %q", got)
	}
}

func TestCancelledContextSkipsIO(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	NewRecorder(context.Background(), dir).Observe(ctx, "Asia/Kolkata")
	if got := Read(context.Background(), dir); got != "" {
		t.Fatalf("cancelled Observe wrote %q", got)
	}
	NewRecorder(context.Background(), dir).Observe(context.Background(), "Asia/Kolkata")
	if got := Read(ctx, dir); got != "" {
		t.Fatalf("cancelled Read = %q", got)
	}
}
