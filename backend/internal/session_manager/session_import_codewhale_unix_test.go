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

func TestCodewhaleImportChecksSourceAndAdoptedLease(t *testing.T) {
	root := t.TempDir()
	const sourceID = "66666666-6666-4666-8666-666666666666"
	const targetID = "77777777-7777-4777-8777-777777777777"
	dir := filepath.Join(root, "sessions", ".late-usage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{Harness: domain.HarnessCodewhale}
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: sourceID, ConfigDir: root}
	rec.Metadata.ProviderConversationID = targetID
	for _, id := range []string{sourceID, targetID} {
		file, err := os.OpenFile(filepath.Join(dir, id+".live"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		owned := codewhaleImportOwnedElsewhere(context.Background(), rec)
		if id == targetID {
			owned = codewhaleAdoptedImportOwnedElsewhere(context.Background(), rec)
		}
		_ = file.Close()
		if !owned {
			t.Fatalf("held native lease %s was not detected", id)
		}
	}
	if codewhaleImportOwnedElsewhere(context.Background(), rec) || codewhaleAdoptedImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("released lease reported as ownership")
	}
}
