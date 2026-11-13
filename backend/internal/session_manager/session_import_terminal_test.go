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
	probes      []ports.SessionRef
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

func (a *terminalImportAgent) NativeConversationExists(_ context.Context, ref ports.SessionRef, _ string, _ map[string]string) (bool, error) {
	a.probes = append(a.probes, ref)
	return true, nil
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
			if a.restores[0].Session.Metadata[ports.MetadataKeyImportSourceNativeID] != "native-terminal" || len(a.probes) != 2 {
				t.Fatalf("restore/preflight import context missing: restores=%#v probes=%#v", a.restores, a.probes)
			}
			for _, probe := range a.probes {
				if probe.Metadata[ports.MetadataKeyImportSourceNativeID] != "native-terminal" || probe.Metadata[ports.MetadataKeyImportConfigDir] != rec.Metadata.ImportSource.ConfigDir || probe.WorkspacePath != child {
					t.Fatalf("strict probe lost import identity/worktree: %#v", probe)
				}
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

func TestTerminalImportAdmissionPreservesIdleSessionGuards(t *testing.T) {
	for _, tc := range []struct {
		name     string
		imported bool
		adopted  bool
		handle   string
		launch   string
	}{
		{name: "ordinary idle terminal without runtime"},
		{name: "adopted import without runtime", imported: true, adopted: true},
		{name: "pending import with runtime", imported: true, handle: "tmux-mer-1", launch: "launch-old"},
		{name: "pending import with handle only", imported: true, handle: "tmux-mer-1"},
		{name: "pending import with launch only", imported: true, launch: "launch-old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &terminalImportAgent{native: true}
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, a)
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessGemini, domain.ActivityIdle
			rec.Metadata.WorkspacePath = t.TempDir()
			rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID = tc.handle, tc.launch
			if tc.imported {
				rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: rec.Metadata.AgentSessionID, Adopted: tc.adopted}
			}
			st.sessions[rec.ID] = rec
			if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); !errors.Is(err, ErrAgentNotExited) {
				t.Fatalf("idle resume error = %v, want ErrAgentNotExited", err)
			}
			if rt.created != 0 || rt.destroyed != 0 || len(a.restores) != 0 || len(a.relocations) != 0 {
				t.Fatalf("refused resume changed runtime or provider: created=%d destroyed=%d restores=%d relocations=%d", rt.created, rt.destroyed, len(a.restores), len(a.relocations))
			}
		})
	}
}
