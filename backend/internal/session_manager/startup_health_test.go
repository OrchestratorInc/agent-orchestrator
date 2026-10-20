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

type healthCheckLauncher struct {
	recordingLauncher
	checks []ChatStart
	err    error
}

func (l *healthCheckLauncher) StartChat(_ context.Context, cfg ChatStart) (ChatStarted, error) {
	l.checks = append(l.checks, cfg)
	return ChatStarted{}, l.err
}

func TestStartupHealthNeverLaunchesStoppedSessions(t *testing.T) {
	ctx := context.Background()
	m, st, rt, ws := newLifecycleManager()
	launcher := &healthCheckLauncher{err: ports.ErrChatHostNotRunning}
	m.chat = launcher
	st.sessions["mer-chat"] = domain.SessionRecord{
		ID: "mer-chat", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "ao/chat/root", ProviderConversationID: "thread-1"},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	st.sessions["mer-tui"] = domain.SessionRecord{
		ID: "mer-tui", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/tui", Branch: "ao/tui/root", RuntimeHandleID: "dead"},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	st.sessions["mer-saved"] = domain.SessionRecord{
		ID: "mer-saved", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/saved", Branch: "ao/saved/root", AgentSessionID: "native"},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-saved"] = []domain.SessionWorktreeRecord{{SessionID: "mer-saved", RepoName: domain.RootWorkspaceRepoName, State: "removed"}}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatalf("startup launched runtime or restored workspace: runtimes=%d restores=%d", rt.created, len(ws.restoreConfigs))
	}
	if len(launcher.checks) != 1 || !launcher.checks[0].ReconnectOnly {
		t.Fatalf("startup did not enforce reconnect-only: %+v", launcher.checks)
	}
	for _, id := range []domain.SessionID{"mer-chat", "mer-tui"} {
		if st.sessions[id].Activity.State != domain.ActivityExited || st.sessions[id].IsTerminated {
			t.Fatalf("%s must stay stopped and resumable: %+v", id, st.sessions[id])
		}
	}
	if !st.sessions["mer-saved"].IsTerminated || len(st.worktrees["mer-saved"]) != 1 {
		t.Fatal("startup changed a shutdown-saved session or its restore marker")
	}
}

func TestStartupHealthInconclusiveChatProbePreservesSession(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	launcher := &healthCheckLauncher{err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	rec := domain.SessionRecord{ID: "mer-chat", ProjectID: "mer", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "ao/chat/root", ProviderConversationID: "thread-1"}, Activity: domain.Activity{State: domain.ActivityActive}}
	st.sessions[rec.ID] = rec
	if err := m.checkSessionHealth(context.Background(), rec); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("error=%v", err)
	}
	if st.sessions[rec.ID].Activity.State != domain.ActivityActive || rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatal("uncertain probe changed activity or launched resources")
	}
}

func TestStartupHealthUnavailableRuntimeProbePreservesSession(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	rt.aliveErr = ports.ErrRuntimeUnavailable
	rec := domain.SessionRecord{
		ID: "mer-tui", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/tui", Branch: "ao/tui/root", RuntimeHandleID: "tmux-1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[rec.ID] = rec

	err := m.checkSessionHealth(context.Background(), rec)
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("error=%v, want runtime unavailable", err)
	}
	if st.sessions[rec.ID].Activity.State != domain.ActivityActive || rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatal("unavailable runtime probe changed activity or launched resources")
	}
}

func TestStartupSwitchRecoveryDoesNotLaunchStoppedSource(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, domain.SessionModeChat} {
		t.Run(string(mode), func(t *testing.T) {
			rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}}
			m, st, _ := newSwitchTestManager(t, rt)
			launcher := &recordingLauncher{}
			m.chat = launcher
			rec := st.sessions["proj-1"]
			rec.Mode = mode
			rec.Activity.State = domain.ActivityExited
			st.sessions[rec.ID] = rec
			sw := domain.AgentSwitch{ID: "startup-switch", SessionID: rec.ID, FromHarness: rec.Harness, TargetHarness: domain.HarnessCodex, State: domain.AgentSwitchSourceStopped, SourceGenerationID: "source", TargetGenerationID: "target"}
			st.switches[sw.ID] = sw
			if err := m.reconcileAgentSwitches(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if rt.created != 0 || len(launcher.started) != 0 {
				t.Fatal("startup switch recovery launched a stopped agent")
			}
			if !st.switches[sw.ID].RequiresSourceRestore() || !m.SessionMutationInProgress(rec.ID) {
				t.Fatal("interrupted switch must retain its explicit recovery fence")
			}
		})
	}
}

func TestStartupHealthCleansOnlyUncommittedSetup(t *testing.T) {
	for _, scenario := range []string{"clean", "dirty", "committed", "runtime", "provider", "setup-done"} {
		t.Run(scenario, func(t *testing.T) {
			m, st, ws, _ := newGitTaskPreparationManager(t)
			rec, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Mode: domain.SessionModeTUI, Harness: domain.HarnessClaudeCode, ClientRequestID: "create-worker", ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepSetup, Status: domain.SessionProvisionStepRunning}}})
			if err != nil {
				t.Fatal(err)
			}
			root, err := ws.Create(t.Context(), ports.WorkspaceConfig{ProjectID: "mer", SessionID: rec.ID, Branch: "ao/login/root", BaseBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			rec.Metadata.WorkspacePath, rec.Metadata.Branch, rec.Metadata.WorkspaceRepoPath = root.Path, root.Branch, root.RepoPath
			switch scenario {
			case "dirty":
				if err := os.WriteFile(filepath.Join(root.Path, "keep"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "committed":
				rec.ClientRequestCommitted = true
			case "runtime":
				rec.Metadata.RuntimeHandleID = "live-runtime"
			case "provider":
				rec.Metadata.ProviderConversationID = "live-provider"
			case "setup-done":
				rec.ProvisionSteps[0].Status = domain.SessionProvisionStepDone
			}
			st.sessions[rec.ID] = rec
			_ = m.checkSessionHealth(t.Context(), rec)
			_, found := st.sessions[rec.ID]
			_, pathErr := os.Stat(root.Path)
			if scenario == "clean" {
				if found || !errors.Is(pathErr, os.ErrNotExist) {
					t.Fatalf("unfinished clean request remains: found=%v path=%v", found, pathErr)
				}
			} else if !found || pathErr != nil {
				t.Fatalf("protected request or workspace lost: found=%v path=%v", found, pathErr)
			}
		})
	}
}
