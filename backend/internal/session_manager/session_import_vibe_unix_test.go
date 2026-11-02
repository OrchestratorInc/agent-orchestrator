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

func TestVibeImportUsesNativeLease(t *testing.T) {
	root := t.TempDir()
	id := "synthetic-session"
	dir := filepath.Join(root, "logs", "session", "active")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(dir, id+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{}
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: id, ConfigDir: root}
	if !vibeImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("missed held native lease")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if vibeImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("released lease still blocks import")
	}
}
