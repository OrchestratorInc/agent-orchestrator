package sessionmanager

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestStartupCueHoldsChatUntilCompletionAndContinuesOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			ctx := context.Background()
			data, workspace := t.TempDir(), t.TempDir()
			st := sqlitetest.MustOpenAt(t, data)
			projectConfig := testRoleAgents()
			legacyCommand, cueCommand := "printf legacy >> setup-order.txt", "printf cue >> setup-order.txt"
			if runtime.GOOS == "windows" {
				legacyCommand, cueCommand = "echo legacy>>setup-order.txt", "echo cue>>setup-order.txt"
			}
			projectConfig.PostCreate = []string{legacyCommand}
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: string(chatTestProject), Path: data, Config: projectConfig, RegisteredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			command := cueCommand
			if fail {
				command = "exit 7"
			}
			if err := st.InsertCue(ctx, domain.Cue{ID: "startup", ProjectID: chatTestProject, Name: "Setup", Type: domain.CueTypeCommand, Command: command, RunOnWorktreeCreation: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			provider := newIntegrationChatConversation("startup-thread")
			var next atomic.Int64
			service := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Drivers: integrationChatRegistry{domain.HarnessCodex: integrationChatDriver{harness: domain.HarnessCodex, start: func() ports.ChatConversation { return provider }}}, Log: slog.New(slog.DiscardHandler), NewID: func() string { return fmt.Sprintf("startup-%d", next.Add(1)) }})
			t.Cleanup(func() { service.StopAll(ctx) })
			m := New(Deps{Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{path: workspace}, Store: st, Messenger: &fakeMessenger{}, Chat: realQueueDrainLauncher{integrationChatLauncher{service: service}}, Lifecycle: lifecycle.New(st, nil), DataDir: data, LookPath: func(string) (string, error) { return "/bin/true", nil }, Logger: slog.New(slog.DiscardHandler)})
			m.browserCapabilities = browsersvc.NewAuthority()
			deferred := deferredBackground(m)
			rec, _, _, err := m.Spawn(ctx, asyncChatSpawnConfig("first task"))
			if err != nil {
				t.Fatal(err)
			}
			(*deferred)[0]() // worktree and provider start; cue execution remains scheduled
			stored, _, err := st.GetSession(ctx, rec.ID)
			if err != nil || !stored.StartupCue.HoldsInput() || stored.ProvisionState != domain.SessionProvisionReady {
				t.Fatalf("session before command: %+v, %v", stored, err)
			}
			provider.mu.Lock()
			sent := len(provider.sent)
			provider.mu.Unlock()
			if sent != 0 {
				t.Fatalf("provider received %d turns before setup", sent)
			}
			if _, err := service.Send(ctx, rec.ID, ports.ChatUserMessage{Text: "second task", Origin: domain.MessageOriginHuman}); err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			sent = len(provider.sent)
			provider.mu.Unlock()
			if sent != 0 {
				t.Fatal("additional prompt bypassed startup hold")
			}
			(*deferred)[1]() // synchronous command completion releases the first queued turn
			stored, _, err = st.GetSession(ctx, rec.ID)
			if err != nil || stored.StartupCue.HoldsInput() {
				t.Fatalf("cue did not release: %+v %v", stored.StartupCue, err)
			}
			want := "succeeded"
			if fail {
				want = "failed"
			}
			if stored.StartupCue.State != want {
				t.Fatalf("state=%s, want %s", stored.StartupCue.State, want)
			}
			order, err := os.ReadFile(filepath.Join(workspace, "setup-order.txt"))
			if err != nil || !strings.Contains(string(order), "legacy") {
				t.Fatalf("legacy postCreate did not run: %q, err=%v", order, err)
			}
			if !fail && (!strings.Contains(string(order), "cue") || strings.Index(string(order), "legacy") > strings.Index(string(order), "cue")) {
				t.Fatalf("setup order = %q, err=%v", order, err)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if len(provider.sent) != 1 || provider.sent[0].Text != "first task" {
				t.Fatalf("released prompts = %+v", provider.sent)
			}
		})
	}
}

func TestStartupCueCommandTimeoutAndOutputBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	command, shell := "sleep 10", "sh"
	if runtime.GOOS == "windows" {
		command, shell = "Start-Sleep -Seconds 10", "powershell.exe"
	}
	started := time.Now()
	result := runWorkspaceCommand(ctx, command, shell, t.TempDir(), nil, 64<<10)
	err := result.Err
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("timeout: %v, duration=%s", err, time.Since(started))
	}
	output := newBoundedWorkspaceOutput(64 << 10)
	_, _ = output.Write([]byte(strings.Repeat("x", 100000)))
	_, _ = output.Write([]byte("tail"))
	if len(output.data) != 64<<10 || !strings.HasSuffix(output.String(), "tail") {
		t.Fatal("output was not bounded to its tail")
	}
}

func TestStartupCueExecutionAdmissionAndChangeEvents(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "project", Path: t.TempDir(), RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "project", Harness: domain.HarnessCodex, Kind: domain.KindWorker, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	run := domain.StartupCueRun{Name: "Setup", State: "pending"}
	if claimed, err := st.ClaimStartupCue(ctx, rec.ID, run); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	seq, err := st.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run.State = "running"
	for attempt := 0; attempt < 2; attempt++ {
		began, err := st.BeginStartupCue(ctx, rec.ID, run)
		if err != nil || began != (attempt == 0) {
			t.Fatalf("execution admission %d: %v %v", attempt, began, err)
		}
	}
	events, err := st.EventsAfter(ctx, seq, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.SessionID == string(rec.ID) && strings.Contains(string(event.Payload), `"startupCue"`) && strings.Contains(string(event.Payload), `"running"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("startup state update did not emit a session change event")
	}
}

func TestStartupCueRecoveryDoesNotRerun(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "project", Path: t.TempDir(), RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "project", Harness: domain.HarnessCodex, Kind: domain.KindWorker, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimStartupCue(ctx, rec.ID, domain.StartupCueRun{Name: "Setup", State: "running"}); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{Store: st, Logger: slog.New(slog.DiscardHandler)})
	rows, err := st.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.recoverStartupCues(ctx, rows); err != nil {
		t.Fatal(err)
	}
	recovered, _, err := st.GetSession(ctx, rec.ID)
	if err != nil || recovered.StartupCue.State != "interrupted" || recovered.StartupCue.HoldsInput() {
		t.Fatalf("recovered: %+v %v", recovered.StartupCue, err)
	}
}

func TestStartupCueHoldsTUIInputAndMessages(t *testing.T) {
	ctx := context.Background()
	data, workspace := t.TempDir(), t.TempDir()
	st := sqlitetest.MustOpenAt(t, data)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: string(chatTestProject), Path: data, Config: testRoleAgents(), RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertCue(ctx, domain.Cue{ID: "startup", ProjectID: chatTestProject, Name: "Setup", Type: domain.CueTypeCommand, Command: "exit 7", RunOnWorktreeCreation: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	messenger := &fakeMessenger{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{path: workspace}, Store: st, Messenger: messenger, Lifecycle: lifecycle.New(st, nil), DataDir: data, LookPath: func(string) (string, error) { return "/bin/true", nil }, Logger: slog.New(slog.DiscardHandler)})
	m.browserCapabilities = browsersvc.NewAuthority()
	deferred := deferredBackground(m)
	cfg := asyncChatSpawnConfig("first task")
	cfg.RequestedMode, cfg.Async = domain.SessionModeTUI, false
	rec, _, _, err := m.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(messenger.msgs) != 0 {
		t.Fatal("TUI got initial task before command completion")
	}
	if release, allowed := m.AcquireSessionInput(rec.ID); allowed {
		release()
		t.Fatal("raw TUI input bypassed startup")
	}
	if err := m.Send(ctx, rec.ID, "second task", nil); err != nil {
		t.Fatal(err)
	}
	if len(messenger.msgs) != 0 {
		t.Fatal("TUI message bypassed startup")
	}
	(*deferred)[0]()
	if len(messenger.msgs) != 2 || messenger.msgs[0] != "first task" || messenger.msgs[1] != "second task" {
		t.Fatalf("TUI deliveries: %v", messenger.msgs)
	}
	if release, allowed := m.AcquireSessionInput(rec.ID); !allowed {
		t.Fatal("input remains held after command failure")
	} else {
		release()
	}
	messages, err := st.ListStartupCueMessages(ctx, rec.ID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("undelivered messages: %+v %v", messages, err)
	}
}
