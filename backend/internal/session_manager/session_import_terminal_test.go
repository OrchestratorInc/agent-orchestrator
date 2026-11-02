package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type terminalImportAgent struct {
	fakeAgent
	restores    []ports.RestoreConfig
	relocations []ports.ImportedSessionRelocation
	native      bool
}

func (a *terminalImportAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	a.restores = append(a.restores, cfg)
	return []string{"gemini", "--resume", cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]}, a.native, nil
}

func (a *terminalImportAgent) RelocateImportedSession(_ context.Context, r ports.ImportedSessionRelocation) error {
	a.relocations = append(a.relocations, r)
	return nil
}

func TestTerminalImportUsesNativeRestoreAndAdoptsWithoutChat(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "native", false: "fresh fallback refused"}[native], func(t *testing.T) {
			a := &terminalImportAgent{native: native}
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, a)
			workspace := t.TempDir()
			child := filepath.Join(workspace, "child")
			if err := os.Mkdir(child, 0o700); err != nil {
				t.Fatal(err)
			}
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessGemini, domain.ActivityIdle
			rec.Metadata = domain.SessionMetadata{
				WorkspacePath: workspace, Branch: "ao/import", AgentSessionID: "native-terminal", ProviderConversationID: "native-terminal",
				ImportSource: &domain.SessionImportSource{NativeID: "native-terminal", ConfigDir: t.TempDir(), Transferred: true, Prepared: true, WorkingSubdir: "child"},
			}
			st.sessions[rec.ID] = rec
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if !native {
				if !errors.Is(err, ErrNotResumable) || !st.sessions[rec.ID].NeedsImportResume() || rt.created != 0 {
					t.Fatalf("fallback accepted: result=%#v err=%v", result, err)
				}
				return
			}
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() {
				t.Fatalf("restore = %#v, %v", result, err)
			}
			if len(a.restores) != 1 || a.restores[0].Session.Metadata[ports.MetadataKeyAgentSessionID] != "native-terminal" || a.restores[0].Session.WorkspacePath != child {
				t.Fatalf("restore configs = %#v", a.restores)
			}
			if len(a.relocations) != 1 || a.relocations[0].LaunchCWD != child || result.Session.Metadata.WorkspacePath != workspace || result.Session.Metadata.ImportSource.ResumeCwd != child {
				t.Fatalf("relocation = %#v, metadata = %#v", a.relocations, result.Session.Metadata)
			}
			// A later native launch uses the pinned directory and never copies again.
			later := st.sessions[rec.ID]
			later.Activity.State = domain.ActivityExited
			later.Metadata.RuntimeLaunchID, later.Metadata.RuntimeHandleID = "", ""
			st.sessions[rec.ID] = later
			if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil || len(a.relocations) != 1 {
				t.Fatalf("reopen = %v, relocations = %#v", err, a.relocations)
			}
		})
	}
}
