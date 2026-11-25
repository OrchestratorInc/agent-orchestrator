//go:build !windows

package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/codewhale"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/runfile"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/sessionimport"
)

type codewhaleRecoveryArchiveStore struct {
	*primeImportIdentityStore
	failCheckpoint bool
	archiveErr     error
	messages       []sessionimport.Message
	snapshots      [][]sessionimport.Message
}

func (s *codewhaleRecoveryArchiveStore) SetPreparedImportIdentity(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, workspace domain.SessionMetadata, now time.Time) (bool, error) {
	if s.failCheckpoint {
		s.failCheckpoint = false
		return false, errors.New("synthetic identity checkpoint failure")
	}
	return s.primeImportIdentityStore.SetPreparedImportIdentity(ctx, id, expected, next, workspace, now)
}
func (s *codewhaleRecoveryArchiveStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return []domain.ProjectRecord{s.projects["mer"]}, nil
}
func (*codewhaleRecoveryArchiveStore) CreateImportedSession(context.Context, domain.SessionRecord, []sessionimport.Message) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, errors.New("unexpected registration during recovery")
}
func (s *codewhaleRecoveryArchiveStore) ReplaceImportedMessages(_ context.Context, _ domain.SessionID, messages []sessionimport.Message) error {
	if s.archiveErr != nil {
		return s.archiveErr
	}
	s.messages = append([]sessionimport.Message(nil), messages...)
	s.snapshots = append(s.snapshots, append([]sessionimport.Message(nil), messages...))
	return nil
}

type codewhaleRecoveryAgent struct {
	*codewhale.Plugin
	prepared []ports.NativeSessionRef
}

func (a *codewhaleRecoveryAgent) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	ref, err := a.Plugin.PrepareImportedSession(ctx, r)
	if err == nil {
		a.prepared = append(a.prepared, ref)
	}
	return ref, err
}

func TestCodeWhaleRecoveredCopyRefreshesMatchingArchiveBeforeLaunch(t *testing.T) {
	for _, failure := range []string{"checkpoint", "archive"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			root, sourceCWD, workspace, bin := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			workspace, err := filepath.EvalSymlinks(workspace)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", t.TempDir())
			t.Setenv("CODEWHALE_HOME", root)
			for _, key := range []string{"CODEWHALE_RUNTIME_DIR", "CODEWHALE_MANAGED_CONFIG_PATH", "DEEPSEEK_MANAGED_CONFIG_PATH"} {
				t.Setenv(key, "")
			}
			script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '0.10.2\\n'; exit 0; fi\nif [ \"$1\" = config ] && [ \"$2\" = get ] && [ \"$3\" = managed_config_path ]; then exit 0; fi\nexit 91\n"
			if err := os.WriteFile(filepath.Join(bin, "codewhale"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			const sourceID = "a386ad88-a036-4f49-8a09-6b63ab71382b"
			message := func(text string) any {
				return map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}
			}
			doc := map[string]any{"schema_version": 1, "metadata": map[string]any{"id": sourceID, "workspace": sourceCWD, "updated_at": "2026-11-03T00:00:00Z"}, "messages": []any{message("RESERVED_HISTORY")}}
			source := filepath.Join(root, "sessions", sourceID+".json")
			writeSource := func() []byte {
				t.Helper()
				body, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, body, 0o600); err != nil {
					t.Fatal(err)
				}
				return body
			}
			original := writeSource()
			agent := &codewhaleRecoveryAgent{Plugin: codewhale.New()}
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, agent)
			archive := &codewhaleRecoveryArchiveStore{primeImportIdentityStore: &primeImportIdentityStore{fakeStore: st}, failCheckpoint: failure == "checkpoint"}
			m.store = archive
			svc := sessionimport.New(archive, m.dataDir, nil)
			m.SetImportArchiveRefresher(svc)
			m.runFilePath = filepath.Join(m.dataDir, "running.json")
			if err := runfile.Write(m.runFilePath, runfile.Info{PID: 42, Port: 43123, StartedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			project := st.projects["mer"]
			project.Path = sourceCWD
			st.projects["mer"] = project
			rec := st.sessions["mer-1"]
			rec.Harness, rec.Mode, rec.Activity.State = domain.HarnessCodewhale, domain.SessionModeTUI, domain.ActivityIdle
			rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/codewhale-recovery", AgentSessionID: sourceID, ProviderConversationID: sourceID, NativeTranscriptPath: source, ImportSource: &domain.SessionImportSource{NativeID: sourceID, ConfigDir: root, CWD: sourceCWD, Prepared: true, Transferred: true}}
			st.sessions[rec.ID] = rec
			if err := svc.RefreshArchive(ctx, rec); err != nil {
				t.Fatal(err)
			}
			if failure == "archive" {
				archive.archiveErr = errors.New("synthetic archive write failure")
			}
			if _, err := m.ResumeAgentWithMode(ctx, rec.ID); err == nil || rt.created != 0 {
				t.Fatalf("failure did not block launch: err=%v launches=%d", err, rt.created)
			}
			if len(agent.prepared) != 1 {
				t.Fatalf("preparation count=%d", len(agent.prepared))
			}
			ref := agent.prepared[0]
			info, err := os.Stat(ref.TranscriptPath)
			if err != nil {
				t.Fatal(err)
			}
			copied, err := os.ReadFile(ref.TranscriptPath)
			if err != nil {
				t.Fatal(err)
			}
			if after, err := os.ReadFile(source); err != nil || string(after) != string(original) {
				t.Fatalf("preparation changed original: %v", err)
			}
			if failure == "checkpoint" && st.sessions[rec.ID].Metadata.ImportSource.ResumeNativeID != "" {
				t.Fatal("failed checkpoint saved prepared identity")
			}
			// The source advances after publication. A restarted adapter must recover
			// the old native copy and archive its turns, never this source-only turn.
			doc["messages"] = append(doc["messages"].([]any), message("SOURCE_ONLY_LATER"))
			advanced := writeSource()
			agent.Plugin, archive.archiveErr = codewhale.New(), nil
			result, err := m.ResumeAgentWithMode(ctx, rec.ID)
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() {
				t.Fatalf("recovery=%+v err=%v", result, err)
			}
			meta := result.Session.Metadata
			binding := meta.ImportSource
			if meta.AgentSessionID != ref.NativeSessionID || meta.ProviderConversationID != ref.NativeSessionID || binding.ResumeNativeID != ref.NativeSessionID || binding.ResumeConfigDir != ref.ConfigDir || binding.ResumeTranscriptPath != ref.TranscriptPath || binding.ResumeCwd != workspace || binding.NativeID != sourceID || binding.ConfigDir != root {
				t.Fatalf("recovery lost identity/provenance: %+v", meta)
			}
			if len(archive.messages) != 1 || archive.messages[0].Text != "RESERVED_HISTORY" {
				t.Fatalf("archive differs from recovered native history: %+v", archive.messages)
			}
			for _, snapshot := range archive.snapshots {
				for _, msg := range snapshot {
					if strings.Contains(msg.Text, "SOURCE_ONLY_LATER") {
						t.Fatal("source-only turn entered a displayed snapshot")
					}
				}
			}
			afterInfo, err := os.Stat(ref.TranscriptPath)
			if err != nil || !os.SameFile(info, afterInfo) {
				t.Fatal("retry republished native copy")
			}
			if after, err := os.ReadFile(ref.TranscriptPath); err != nil || string(after) != string(copied) {
				t.Fatalf("retry rewrote recovered native copy: %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(root, "sessions"))
			if err != nil || len(entries) != 2 {
				t.Fatalf("expected source + exactly one copy: entries=%d err=%v", len(entries), err)
			}
			snapshots := len(archive.snapshots)
			later := st.sessions[rec.ID]
			later.Activity.State = domain.ActivityExited
			later.Metadata.RuntimeHandleID, later.Metadata.RuntimeLaunchID, later.Metadata.AgentSessionIDLaunchID = "", "", ""
			st.sessions[rec.ID] = later
			reopened, err := m.ResumeAgentWithMode(ctx, rec.ID)
			if err != nil || reopened.Session.Metadata.AgentSessionID != ref.NativeSessionID || reopened.Session.Metadata.ProviderConversationID != ref.NativeSessionID || len(archive.snapshots) != snapshots {
				t.Fatalf("same-ID reopen/archive=%+v snapshots=%d err=%v", reopened, len(archive.snapshots), err)
			}
			if after, err := os.ReadFile(source); err != nil || string(after) != string(advanced) {
				t.Fatalf("recovery modified original source: %v", err)
			}
		})
	}
}
