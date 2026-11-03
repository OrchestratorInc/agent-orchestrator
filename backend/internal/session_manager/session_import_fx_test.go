//go:build !windows

package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestFXImportedWriterLockBeforeAndAfterAdoption(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			id := "synthetic521"
			path := filepath.Join(root, "sessions", id, "session.lock")
			if version == "v2" {
				path = filepath.Join(root, "sessions", "v2", id, "lock")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			rec := domain.SessionRecord{Harness: domain.HarnessFX}
			rec.Metadata.ImportSource = &domain.SessionImportSource{ConfigDir: root, NativeID: id}
			if !importOwnedElsewhere(context.Background(), rec) || !adoptedOwnedElsewhere(context.Background(), rec) {
				t.Fatal("held writer lock ignored")
			}
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
				t.Fatal(err)
			}
			if fxImportOwnedElsewhere(context.Background(), rec) {
				t.Fatal("stale lock file mistaken for a writer")
			}
		})
	}
}
