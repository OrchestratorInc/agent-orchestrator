package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type deepseekPreflightAgent struct {
	transitionAgent
	seen ports.RestoreConfig
}

func (a *deepseekPreflightAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	a.seen = cfg
	return nil, false, ports.ErrNativeHistoryUnavailable
}

func TestDeepSeekTerminalPreflightCarriesPreparedIdentityAndLaunchDirectory(t *testing.T) {
	m, store, _, _, log := newTransitionManager(t, domain.SessionModeChat)
	rec := store.sessions["session-1"]
	workspace := t.TempDir()
	child := filepath.Join(workspace, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	prepared := t.TempDir()
	rec.Harness, rec.Metadata.WorkspacePath = domain.HarnessDeepSeek, workspace
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "original-native", ConfigDir: t.TempDir(), ResumeNativeID: "native-1", ResumeConfigDir: prepared, ResumeCwd: child, Adopted: true}
	agent := &deepseekPreflightAgent{}
	err := m.preflightTerminalLaunch(context.Background(), agent, rec, store.projects["proj"], "native-1", map[string]string{"DSH_HOME": prepared})
	if !errors.Is(err, ports.ErrNativeHistoryUnavailable) {
		t.Fatalf("preflight = %v", err)
	}
	meta := agent.seen.Session.Metadata
	if meta[ports.MetadataKeyImportSourceNativeID] != "original-native" || meta[ports.MetadataKeyImportResumeNativeID] != "native-1" || meta[ports.MetadataKeyImportConfigDir] != prepared || agent.seen.Session.WorkspacePath != child {
		t.Fatalf("preflight lost native import identity: %+v", agent.seen.Session)
	}
	if len(*log) != 0 {
		t.Fatalf("source runtime changed before refusal: %v", *log)
	}
}
