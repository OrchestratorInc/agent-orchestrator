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

func TestOMPImportOpenElsewhereDetectsHeldLocksBeforeAndAfterAdoption(t *testing.T) {
	root := t.TempDir()
	const id = "01a17cf3-99d0-76cd-b084-4cdce17a0fec"
	rec := domain.SessionRecord{Harness: domain.HarnessOMP}
	rec.Metadata.ImportSource = &domain.SessionImportSource{ConfigDir: root, NativeID: id}
	for _, folder := range []string{"original", "ao-copy"} {
		t.Run(folder, func(t *testing.T) {
			lock := filepath.Join(root, "sessions", folder, ".2026-10-27_"+id+".jsonl.lock.os")
			if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ctx := context.Background()
			if importOwnedElsewhere(ctx, rec) || adoptedOwnedElsewhere(ctx, rec) {
				t.Fatal("an unlocked stale file was treated as a live owner")
			}
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			if !importOwnedElsewhere(ctx, rec) || !adoptedOwnedElsewhere(ctx, rec) {
				t.Fatal("the live OMP lock was not detected")
			}
			other := rec
			other.Metadata.ImportSource = &domain.SessionImportSource{ConfigDir: root, NativeID: "00000000-0000-7000-8000-000000000000"}
			if importOwnedElsewhere(ctx, other) || adoptedOwnedElsewhere(ctx, other) {
				t.Fatal("another conversation's lock blocked the import")
			}
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
				t.Fatal(err)
			}
			if importOwnedElsewhere(ctx, rec) || adoptedOwnedElsewhere(ctx, rec) {
				t.Fatal("a released OMP lock was treated as live")
			}
		})
	}
}
