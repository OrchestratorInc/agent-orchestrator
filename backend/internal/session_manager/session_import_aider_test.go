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
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
)

type aiderNativeArchiveStore struct {
	*fakeStore
	messages []sessionimport.Message
}

func (s *aiderNativeArchiveStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return []domain.ProjectRecord{s.projects["mer"]}, nil
}

func (s *aiderNativeArchiveStore) CreateImportedSession(_ context.Context, rec domain.SessionRecord, messages []sessionimport.Message) (domain.SessionRecord, bool, error) {
	rec.ID = "mer-1"
	s.sessions[rec.ID], s.messages = rec, append([]sessionimport.Message(nil), messages...)
	return rec, true, nil
}

func (s *aiderNativeArchiveStore) ReplaceImportedMessages(_ context.Context, _ domain.SessionID, messages []sessionimport.Message) error {
	s.messages = append([]sessionimport.Message(nil), messages...)
	return nil
}

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
			if provenance.ConfigDir != source || provenance.ResumeConfigDir != workspace || provenance.ResumeTranscriptPath != target || result.Session.Metadata.AgentSessionID != id {
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
			if workspace == source || rt.lastCfg.WorkspacePath != workspace || !prepared.Transferred || !prepared.Prepared || prepared.ResumeNativeID != id || prepared.ResumeConfigDir != workspace || prepared.ConfigDir != source {
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

func TestAiderPreparedRetryKeepsArchiveAlignedWithNativeSnapshot(t *testing.T) {
	source, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(binDir, "aider"), []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "#### original prompt\n\nOriginal answer\n"
	path := filepath.Join(source, ".aider.chat.history.md")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{createErr: errors.New("synthetic launch failure after preparation")}
	m, st, _ := newExitedResumeManager(t, rt, aider.New())
	project := st.projects["mer"]
	project.Path, project.Kind = source, domain.ProjectKindSingleRepo
	st.projects["mer"] = project
	archive := &aiderNativeArchiveStore{fakeStore: st}
	svc := sessionimport.New(archive, m.dataDir, nil)
	m.SetImportArchiveRefresher(svc)
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %+v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %+v, %v", results, err)
	}
	rec := st.sessions["mer-1"]
	rec.Metadata.WorkspacePath, rec.Metadata.Branch = workspace, "ao/import"
	rec.Metadata.ImportSource.Transferred, rec.Metadata.ImportSource.Prepared = true, true
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil {
		t.Fatal("synthetic first launch did not fail")
	}
	prepared := st.sessions[rec.ID].Metadata.ImportSource
	if prepared.ResumeNativeID == "" || prepared.ResumeTranscriptPath == "" || prepared.Adopted || prepared.Fingerprint != aider.NativeHistoryFingerprint([]byte(original)) {
		t.Fatalf("failure did not retain the prepared native snapshot: %+v", prepared)
	}
	changed := original + "#### later source turn\n\nNot in the prepared context\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	rt.createErr = nil
	result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
	if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || rt.created != 1 {
		t.Fatalf("prepared native retry = %#v, %v", result, err)
	}
	if len(archive.messages) != 2 || archive.messages[0].Text != "original prompt" || archive.messages[1].Text != "Original answer" {
		t.Fatalf("archive diverged from restored native context: %+v", archive.messages)
	}
	if !strings.Contains(strings.Join(rt.lastCfg.Argv, " "), "--chat-history-file "+prepared.ResumeTranscriptPath) {
		t.Fatalf("retry did not select the prepared native snapshot: %v", rt.lastCfg.Argv)
	}
	data, err := os.ReadFile(prepared.ResumeTranscriptPath)
	if err != nil || string(data) != original || rt.lastCfg.WorkspacePath != workspace || result.Session.Metadata.AgentSessionID != prepared.ResumeNativeID {
		t.Fatalf("prepared native context changed: %q, %v, %+v", data, err, rt.lastCfg)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != changed {
		t.Fatalf("source changed during retry: %q, %v", data, err)
	}
}

func TestAiderRecoversPublishedHistoryAfterLostCheckpoint(t *testing.T) {
	for _, sourceState := range []string{"removed", "changed"} {
		for _, damage := range []string{"none", "missing proof", "bad proof", "corrupt copy"} {
			t.Run(sourceState+"/"+damage, func(t *testing.T) {
				source, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
				t.Setenv("HOME", t.TempDir())
				t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
				if err := os.WriteFile(filepath.Join(binDir, "aider"), []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				original := "#### original prompt\n\nOriginal answer\n"
				path := filepath.Join(source, ".aider.chat.history.md")
				if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
					t.Fatal(err)
				}
				rt := &fakeRuntime{}
				m, st, _ := newExitedResumeManager(t, rt, aider.New())
				project := st.projects["mer"]
				project.Path, project.Kind = source, domain.ProjectKindSingleRepo
				st.projects["mer"] = project
				archive := &aiderNativeArchiveStore{fakeStore: st}
				svc := sessionimport.New(archive, m.dataDir, nil)
				m.SetImportArchiveRefresher(svc)
				preview, err := svc.Scan(context.Background())
				if err != nil || len(preview.Candidates) != 1 {
					t.Fatalf("scan = %+v, %v", preview, err)
				}
				results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
				if err != nil || len(results) != 1 || results[0].Status != "created" {
					t.Fatalf("import = %+v, %v", results, err)
				}
				rec := st.sessions["mer-1"]
				rec.Metadata.WorkspacePath, rec.Metadata.Branch = workspace, "ao/import"
				rec.Metadata.ImportSource.Transferred, rec.Metadata.ImportSource.Prepared = true, true
				st.sessions[rec.ID] = rec
				store := &failingImportWorkspaceStore{fakeStore: st, failAt: 1}
				m.store = store
				if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil || store.calls != 1 || rt.created != 0 {
					t.Fatalf("checkpoint failure was not isolated: %v, calls=%d launches=%d", err, store.calls, rt.created)
				}
				pending := st.sessions[rec.ID].Metadata.ImportSource
				if pending.ResumeNativeID != "" || pending.ResumeCwd != "" {
					t.Fatal("failed checkpoint unexpectedly persisted")
				}
				if sourceState == "removed" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte(original+"#### later source turn\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				proof := filepath.Join(workspace, ".aider.chat.history.md.ao-import.json")
				target := filepath.Join(workspace, ".aider.chat.history.md")
				want := original
				switch damage {
				case "missing proof":
					if err := os.Remove(proof); err != nil {
						t.Fatal(err)
					}
				case "bad proof":
					if err := os.WriteFile(proof, []byte(`{}`), 0o600); err != nil {
						t.Fatal(err)
					}
				case "corrupt copy":
					want = "#### altered private context\n"
					if err := os.WriteFile(target, []byte(want), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				// A fresh adapter must establish recovery from disk, not memory.
				m.agents = singleAgent{agent: aider.New()}
				result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
				if damage == "none" {
					if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || result.Session.Metadata.AgentSessionID != rec.Metadata.AgentSessionID || result.Session.Metadata.NativeTranscriptPath != target || rt.lastCfg.WorkspacePath != workspace || rt.created != 1 {
						t.Fatalf("private recovery = %#v, %v", result, err)
					}
				} else if err == nil || rt.created != 0 || !st.sessions[rec.ID].NeedsImportResume() {
					t.Fatalf("unproven private recovery = %#v, %v, launches=%d", result, err, rt.created)
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != want || len(archive.messages) != 2 || archive.messages[0].Text != "original prompt" || archive.messages[1].Text != "Original answer" {
					t.Fatalf("native/archive snapshot changed: %q, %v, %+v", data, err, archive.messages)
				}
			})
		}
	}
}
