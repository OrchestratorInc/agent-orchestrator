//go:build !windows

package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type aiderSnapshotArchive struct{}

func (aiderSnapshotArchive) RefreshArchive(_ context.Context, rec domain.SessionRecord) error {
	data, err := os.ReadFile(rec.Metadata.NativeTranscriptPath)
	if err != nil {
		return err
	}
	if aider.NativeHistoryFingerprint(data) != rec.Metadata.ImportSource.Fingerprint {
		return errors.New("synthetic selected source changed")
	}
	return nil
}

type aiderTransferredAgent struct {
	*aider.Plugin
	sawTransferredHistory bool
}

func (a *aiderTransferredAgent) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	_, historyErr := os.Stat(filepath.Join(r.LaunchCWD, ".aider.chat.history.md"))
	_, markerErr := os.Stat(filepath.Join(r.LaunchCWD, ".aider.chat.history.md.ao-import"))
	a.sawTransferredHistory = historyErr == nil && errors.Is(markerErr, os.ErrNotExist)
	return a.Plugin.PrepareImportedSession(ctx, r)
}

func TestAiderFirstOpenCopiesNativeHistoryAndRefusesExistingHistory(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "native copy", true: "existing history refused"}[existing], func(t *testing.T) {
			source, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := os.WriteFile(filepath.Join(binDir, "aider"), []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			body := "#### make a greeting\n\nHello\n"
			path := filepath.Join(source, ".aider.chat.history.md")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(workspace, ".aider.chat.history.md")
			if existing {
				if err := os.WriteFile(target, []byte("#### unrelated worktree chat\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			id := aider.NativeHistoryID(source)
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, aider.New())
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessAider, domain.ActivityIdle
			rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id, NativeTranscriptPath: path,
				ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: source, CWD: source, Transferred: true, Prepared: true}}
			st.sessions[rec.ID] = rec
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if existing {
				if err == nil || rt.created != 0 || !st.sessions[rec.ID].NeedsImportResume() {
					t.Fatalf("existing history was accepted = %#v, %v, created=%d", result, err, rt.created)
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "#### unrelated worktree chat\n" {
					t.Fatalf("existing history changed = %q, %v", data, err)
				}
				return
			}
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || rt.created != 1 {
				t.Fatalf("native Aider first open = %#v, %v, created=%d", result, err, rt.created)
			}
			argv := strings.Join(rt.lastCfg.Argv, " ")
			if !strings.Contains(argv, "--restore-chat-history") || !strings.Contains(argv, "--chat-history-file "+target) || rt.lastCfg.WorkspacePath != workspace {
				t.Fatalf("wrong native/worktree launch = %#v", rt.lastCfg)
			}
			provenance := result.Session.Metadata.ImportSource
			if provenance.ConfigDir != source || provenance.ResumeConfigDir != workspace || provenance.ResumeTranscriptPath != target || provenance.SourceTranscriptPath != path || result.Session.Metadata.AgentSessionID != id {
				t.Fatalf("native copy lost provenance or identity = %+v", result.Session.Metadata)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != body {
				t.Fatalf("source history changed = %q, %v", data, err)
			}
		})
	}
}

func TestAiderFreshImportReusesOnlyPinnedHistoryFromGitTransfer(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-ignored history", true: "tracked history"}[tracked], func(t *testing.T) {
			_, _, realWorkspace, source := newGitTaskPreparationManager(t)
			binDir := t.TempDir()
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := os.WriteFile(filepath.Join(binDir, "aider"), []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			body := "#### make a greeting\n\nHello\n"
			path := filepath.Join(source, ".aider.chat.history.md")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if tracked {
				runManagerGit(t, source, "add", ".aider.chat.history.md")
				runManagerGit(t, source, "commit", "-m", "synthetic native history")
			}
			agent := &aiderTransferredAgent{Plugin: aider.New()}
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, agent)
			m.workspace = realWorkspace
			m.SetImportArchiveRefresher(aiderSnapshotArchive{})
			project := st.projects["mer"]
			project.Path, project.Kind, project.Config.DefaultBranch = source, domain.ProjectKindSingleRepo, "main"
			st.projects["mer"] = project
			id := aider.NativeHistoryID(source)
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessAider, domain.ActivityIdle
			rec.Metadata = domain.SessionMetadata{Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id, NativeTranscriptPath: path,
				ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: source, CWD: source, Fingerprint: aider.NativeHistoryFingerprint([]byte(body))}}
			st.sessions[rec.ID] = rec
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || rt.created != 1 || !agent.sawTransferredHistory {
				t.Fatalf("fresh Git import = %#v, %v, created=%d transferred=%v", result, err, rt.created, agent.sawTransferredHistory)
			}
			workspace := result.Session.Metadata.WorkspacePath
			prepared := result.Session.Metadata.ImportSource
			if workspace == source || rt.lastCfg.WorkspacePath != workspace || !prepared.Transferred || !prepared.Prepared || prepared.ResumeNativeID != id || prepared.ResumeConfigDir != workspace || prepared.ConfigDir != source || prepared.SourceTranscriptPath != path {
				t.Fatalf("wrong worktree/provenance = %+v, %+v", result.Session.Metadata, rt.lastCfg)
			}
			argv := strings.Join(rt.lastCfg.Argv, " ")
			if !strings.Contains(argv, "--restore-chat-history --chat-history-file "+filepath.Join(workspace, ".aider.chat.history.md")) {
				t.Fatalf("wrong native history argv = %v", rt.lastCfg.Argv)
			}
			for _, filename := range []string{path, filepath.Join(workspace, ".aider.chat.history.md")} {
				data, err := os.ReadFile(filename)
				if err != nil || string(data) != body {
					t.Fatalf("native history changed at %s = %q, %v", filename, data, err)
				}
			}
		})
	}
}
