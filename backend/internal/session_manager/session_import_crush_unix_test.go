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

func TestCrushImportOwnershipFollowsRelocatedStore(t *testing.T) {
	source, workspace := t.TempDir(), t.TempDir()
	id := "12345678-1234-1234-1234-123456789012"
	lock := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(dir, "crush.lock"))
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
		})
	}
	lock(source)
	rec := domain.SessionRecord{}
	rec.Metadata.NativeTranscriptPath = filepath.Join(source, "crush.db", id)
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: id, ConfigDir: t.TempDir()}
	if !crushImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("source writer lock was not detected")
	}
	rec.Metadata.ImportSource.ResumeCwd = workspace
	if crushImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("source writer must not claim the relocated store")
	}
	lock(filepath.Join(workspace, ".crush"))
	if !crushImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("relocated writer lock was not detected")
	}
}
