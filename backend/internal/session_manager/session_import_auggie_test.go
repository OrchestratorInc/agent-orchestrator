package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/auggie"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuggieImportRestorePinsOriginalCacheDespiteLaunchHome(t *testing.T) {
	bin := t.TempDir()
	name := "auggie"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Resolution only: the fixture executable must never be started here.
	t.Setenv("PATH", bin)
	original, prepared, worktree := t.TempDir(), t.TempDir(), t.TempDir()
	launchEnv := map[string]string{"HOME": t.TempDir()}
	for _, adopted := range []bool{false, true} {
		for _, preparedCopy := range []bool{false, true} {
			source := &domain.SessionImportSource{NativeID: "11111111-2222-4333-8444-555555555555", ConfigDir: original, Adopted: adopted}
			want := original
			if preparedCopy {
				source.ResumeConfigDir, want = prepared, prepared
			}
			meta := domain.SessionMetadata{AgentSessionID: source.NativeID, ProviderConversationID: source.NativeID, ImportSource: source}
			argv, _, mode, err := restoreArgv(context.Background(), auggie.New(), "imported", worktree, meta, "", "", ports.AgentConfig{}, domain.KindWorker, domain.HarnessAuggie, t.TempDir(), launchEnv)
			if err != nil || mode != RestoreModeNative {
				t.Fatalf("adopted=%v prepared=%v: mode=%s err=%v", adopted, preparedCopy, mode, err)
			}
			joined := strings.Join(argv, "\x00")
			if !strings.Contains(joined, "--augment-cache-dir\x00"+want) || !strings.Contains(joined, "--workspace-root\x00"+worktree) || !strings.Contains(joined, "--resume\x00"+source.NativeID) {
				t.Fatalf("adopted=%v prepared=%v restore=%v", adopted, preparedCopy, argv)
			}
		}
	}
}
