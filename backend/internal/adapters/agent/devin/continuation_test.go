package devin

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportNativeHomeAndMissingSession(t *testing.T) {
	p := New()
	root, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"XDG_DATA_HOME": "/custom/data"})
	if err != nil || root != filepath.Join("/custom/data", "devin", "cli") {
		t.Fatalf("root=%q err=%v", root, err)
	}
	got, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{ConfigDir: t.TempDir(), NativeSessionID: "gentle-otter"})
	if err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("probe=%s err=%v", got, err)
	}
}
