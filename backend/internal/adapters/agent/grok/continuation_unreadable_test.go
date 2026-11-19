//go:build !windows

package grok

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const grokLookupTestID = "00000000-0000-7000-8000-0000000000aa"

func requireUnreadableGrokDirectory(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("directory permissions are not enforced for this user")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
}

func writeGrokLookupTranscript(t *testing.T, root, bucket string) string {
	t.Helper()
	target := filepath.Join(root, "sessions", bucket, grokLookupTestID, "updates.jsonl")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

// An unreadable unrelated bucket must not block a readable selected
// conversation, whether the walk reaches it before or after the target.
func TestGrokLocateSkipsUnreadableSibling(t *testing.T) {
	for _, tc := range []struct{ name, blocked, bucket string }{
		{name: "sibling before target", blocked: "%2Faaa", bucket: "%2Fzzz"},
		{name: "sibling after target", blocked: "%2Fzzz", bucket: "%2Faaa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			target := writeGrokLookupTranscript(t, root, tc.bucket)
			blocked := filepath.Join(root, "sessions", tc.blocked)
			if err := os.MkdirAll(blocked, 0o700); err != nil {
				t.Fatal(err)
			}
			requireUnreadableGrokDirectory(t, blocked)
			ref := ports.NativeSessionRef{NativeSessionID: grokLookupTestID, ConfigDir: root}
			p := New()
			if got, ok, err := p.LocateTranscript(context.Background(), ref); err != nil || !ok || got != target {
				t.Fatalf("readable target transcript = %q, %v, %v", got, ok, err)
			}
			if got, err := p.ProbeNativeSession(context.Background(), ref); err != nil || got != ports.NativeSessionAvailabilityAvailable {
				t.Fatalf("readable target availability = %v, %v", got, err)
			}
		})
	}
}

func TestGrokLocateReportsUnreadableSessionsRoot(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	if err := os.Mkdir(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	requireUnreadableGrokDirectory(t, sessions)
	ref := ports.NativeSessionRef{NativeSessionID: grokLookupTestID, ConfigDir: root}
	if _, ok, err := New().LocateTranscript(context.Background(), ref); ok || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unreadable root: found=%v, err=%v", ok, err)
	}
}

func TestGrokLocatePreservesCancellation(t *testing.T) {
	root := t.TempDir()
	writeGrokLookupTranscript(t, root, "%2Fwork")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ref := ports.NativeSessionRef{NativeSessionID: grokLookupTestID, ConfigDir: root}
	if _, ok, err := New().LocateTranscript(ctx, ref); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lookup: found=%v, err=%v", ok, err)
	}
}
