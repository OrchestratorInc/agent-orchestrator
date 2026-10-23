package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/freebuff"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestBusyHarnessSessionsNamesWhyEachMustWait(t *testing.T) {
	mgr, store, _ := newChatManager(&recordingLauncher{})
	now := time.Now()
	seedChatResumeSession(store, domain.ActivityIdle)
	idle := store.sessions["mer-1"]
	for id, change := range map[domain.SessionID]func(*domain.SessionRecord){
		"working":  func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityActive },
		"approval": func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityWaitingInput },
		"starting": func(r *domain.SessionRecord) { r.ProvisionState = domain.SessionProvisionProvisioning },
		// Sessions without a process never hold the update back.
		"asleep":     func(r *domain.SessionRecord) { r.HibernatedAt = &now },
		"exited":     func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited },
		"terminated": func(r *domain.SessionRecord) { r.IsTerminated = true },
		"other": func(r *domain.SessionRecord) {
			r.Harness = domain.HarnessClaudeCode
			r.Activity.State = domain.ActivityActive
		},
	} {
		rec := idle
		rec.ID = id
		change(&rec)
		store.sessions[id] = rec
	}
	busy, err := mgr.BusyHarnessSessions(context.Background(), domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	got := map[domain.SessionID]domain.SessionWaitReason{}
	for _, wait := range busy {
		got[wait.SessionID] = wait.Reason
	}
	want := map[domain.SessionID]domain.SessionWaitReason{
		"working": domain.SessionWaitWorking, "approval": domain.SessionWaitApproval, "starting": domain.SessionWaitStarting,
	}
	if len(got) != len(want) {
		t.Fatalf("busy = %v, want %v", got, want)
	}
	for id, reason := range want {
		if got[id] != reason {
			t.Fatalf("busy = %v, want %v", got, want)
		}
	}
}

// idleWorkloadRuntime reports that no terminal runs a command.
type idleWorkloadRuntime struct{ *fakeRuntime }

func (idleWorkloadRuntime) RunningWorkloadCommands(context.Context, ports.RuntimeHandle, ports.SupervisedProcessRef) (int, error) {
	return 0, nil
}

func TestBusyHarnessSessionsKeepsATerminalThatCannotResume(t *testing.T) {
	mgr, store, rt := newChatManager(&recordingLauncher{})
	mgr.runtime = idleWorkloadRuntime{rt}
	terminal := func(id domain.SessionID, agentSessionID string) domain.SessionRecord {
		return domain.SessionRecord{ID: id, ProjectID: chatTestProject, Kind: domain.KindWorker,
			Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
			Activity: domain.Activity{State: domain.ActivityIdle},
			Metadata: domain.SessionMetadata{WorkspacePath: "/ws/" + string(id), Branch: "ao/" + string(id),
				RuntimeHandleID: "pane-" + string(id), AgentSessionID: agentSessionID}}
	}
	store.sessions["resumable"] = terminal("resumable", "native-1")
	// No native conversation id: stopping it would leave nothing to resume.
	store.sessions["fresh-only"] = terminal("fresh-only", "")

	busy, err := mgr.BusyHarnessSessions(context.Background(), domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(busy) != 1 || busy[0].SessionID != "fresh-only" || busy[0].Reason != domain.SessionWaitResume {
		t.Fatalf("busy = %+v, want only fresh-only waiting to resume", busy)
	}
}

func TestBusyHarnessSessionsUsesFreebuffProjectHistory(t *testing.T) {
	for _, tc := range []struct {
		name           string
		projectHistory bool
		wantBlocked    bool
	}{
		{name: "project history with unrelated daemon chat", projectHistory: true},
		{name: "daemon history cannot replace missing project chat", wantBlocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, store, rt := newChatManager(&recordingLauncher{})
			mgr.runtime = idleWorkloadRuntime{rt}
			mgr.dataDir = t.TempDir()
			workspace, projectDir, daemonDir := t.TempDir(), t.TempDir(), t.TempDir()
			home, binDir := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("FREEBUFF_CONFIG_DIR", daemonDir)
			t.Setenv("PATH", binDir)
			binaryName := "freebuff"
			if runtime.GOOS == "windows" {
				binaryName += ".cmd"
				t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
			}
			binary := filepath.Join(binDir, binaryName)
			if err := os.WriteFile(binary, []byte("not executed by this test\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			agent := freebuff.New()
			if got, err := agent.ResolveBinary(context.Background()); err != nil || got != binary {
				t.Fatalf("cache isolated Freebuff binary: path=%q err=%v", got, err)
			}
			mgr.agents = singleAgent{agent: agent}
			project := store.projects[string(chatTestProject)]
			project.Config.Env = map[string]string{"FREEBUFF_CONFIG_DIR": projectDir}
			store.projects[string(chatTestProject)] = project
			const preferences = `{"freebuffModel":"deepseek/deepseek-v4-flash","adsEnabled":false}`
			for _, dir := range []string{projectDir, daemonDir} {
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(preferences), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeChat := func(dir, id, projectRoot string) {
				t.Helper()
				chatDir := filepath.Join(dir, "projects", filepath.Base(workspace), "chats", id)
				if err := os.MkdirAll(chatDir, 0o700); err != nil {
					t.Fatal(err)
				}
				state, err := json.Marshal(map[string]any{"sessionState": map[string]any{
					"fileContext": map[string]string{"projectRoot": projectRoot},
				}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(chatDir, "run-state.json"), state, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			daemonWorkspace := workspace
			if tc.projectHistory {
				writeChat(projectDir, "project-chat", workspace)
				daemonWorkspace = t.TempDir()
			}
			writeChat(daemonDir, "daemon-chat", daemonWorkspace)
			store.sessions["freebuff-1"] = domain.SessionRecord{
				ID: "freebuff-1", ProjectID: chatTestProject, Kind: domain.KindWorker,
				Harness: domain.HarnessFreebuff, Mode: domain.SessionModeTUI,
				Activity: domain.Activity{State: domain.ActivityIdle},
				Metadata: domain.SessionMetadata{WorkspacePath: workspace, RuntimeHandleID: "freebuff-pane"},
			}
			busy, err := mgr.BusyHarnessSessions(context.Background(), domain.HarnessFreebuff)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantBlocked {
				if len(busy) != 1 || busy[0].SessionID != "freebuff-1" || busy[0].Reason != domain.SessionWaitResume {
					t.Fatalf("busy=%+v, want missing project history to prevent stopping", busy)
				}
			} else if len(busy) != 0 {
				t.Fatalf("busy=%+v, want project history to allow update", busy)
			}
			for _, dir := range []string{projectDir, daemonDir} {
				data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
				if err != nil || string(data) != preferences {
					t.Fatalf("resume eligibility changed native preferences: %q err=%v", data, err)
				}
			}
		})
	}
}

func TestResumeAfterUpdateDeliversWhatQueuedWhileStopped(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityIdle)
	rec := store.sessions["mer-1"]
	at := time.Now().UTC()
	rec.HibernatedAt = &at
	store.sessions[rec.ID] = rec

	if err := mgr.ResumeHarnessSessions(context.Background(), []domain.SessionID{rec.ID}); err != nil {
		t.Fatal(err)
	}
	if len(launcher.started) != 1 || len(launcher.drained) != 1 || launcher.drained[0] != rec.ID {
		t.Fatalf("started=%d drained=%v, want one start and its queue delivered", len(launcher.started), launcher.drained)
	}
}

func TestFailedResumeAfterUpdateIsRetriedWithoutTheUser(t *testing.T) {
	providerErr := errors.New("provider failed to start")
	launcher := &recordingLauncher{startErr: providerErr}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityIdle)
	rec := store.sessions["mer-1"]
	at := time.Now().UTC()
	rec.HibernatedAt = &at
	store.sessions[rec.ID] = rec

	if err := mgr.ResumeHarnessSessions(context.Background(), []domain.SessionID{rec.ID}); !errors.Is(err, providerErr) {
		t.Fatalf("resume error = %v, want the provider failure", err)
	}
	if pending := mgr.versionRetries.pending(); len(pending) != 1 || pending[0] != rec.ID {
		t.Fatalf("retries = %v, want the session enrolled", pending)
	}
	// The provider recovers; the retry sweep starts it again.
	launcher.startErr = nil
	if err := mgr.RetryVersionMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.started) != 2 || len(mgr.versionRetries.pending()) != 0 {
		t.Fatalf("starts=%d retries=%v, want a second start and nothing left to retry", len(launcher.started), mgr.versionRetries.pending())
	}
}

func TestRetryAfterUpdateNeverFallsBackToFreshTerminal(t *testing.T) {
	rt := &fakeRuntime{}
	agent := lostConversationAgent{recordingAgent: &recordingAgent{}}
	mgr, store, _ := newExitedResumeManager(t, rt, agent)
	rec := store.sessions["mer-1"]
	// An in-place update stopped this launch before marking live sessions stale.
	rec.StaleBinary = nil
	store.sessions[rec.ID] = rec
	ctx := context.Background()
	if err := mgr.ResumeHarnessSessions(ctx, []domain.SessionID{rec.ID}); !errors.Is(err, ErrNotResumable) {
		t.Fatalf("initial strict resume = %v, want ErrNotResumable", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := mgr.RetryVersionMoves(ctx); !errors.Is(err, ErrNotResumable) {
			t.Fatalf("retry %d = %v, want ErrNotResumable", attempt+1, err)
		}
		if rt.created != 0 {
			t.Fatalf("retry launched %d fresh terminals after losing native history", rt.created)
		}
		if pending := mgr.versionRetries.pending(); len(pending) != 1 || pending[0] != rec.ID {
			t.Fatalf("retry intent lost: %v", pending)
		}
	}
}
