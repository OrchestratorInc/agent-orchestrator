//go:build !windows

package kimi

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestKimiNativePipeDoesNotBlock(t *testing.T) {
	for _, kind := range []string{"state", "sessions"} {
		t.Run(kind, func(t *testing.T) {
			r := kimiImportFixture(t)
			path := filepath.Join(kimiSessionDirForWire(r.TranscriptPath), "state.json")
			if kind == "sessions" {
				path = filepath.Join(r.ConfigDir, "sessions")
			}
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { _, err := ReadImportedSession(t.Context(), r.ConfigDir, r.NativeSessionID); result <- err }()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("pipe accepted as native state")
				}
			case <-time.After(time.Second):
				t.Fatal("native pipe blocked import")
			}
		})
	}
}

func TestKimiPermissionFailureRemainsUnknown(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode bits")
	}
	root := t.TempDir()
	path := writeKimiTranscript(t, root, "wd_private_000000000000", kimiTestSession)
	dir := filepath.Dir(filepath.Dir(path))
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	availability, err := New().ProbeNativeSession(t.Context(), ports.NativeSessionRef{ConfigDir: root, NativeSessionID: kimiTestSession})
	if err == nil || availability != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("permission failure became missing history: %s %v", availability, err)
	}
}
