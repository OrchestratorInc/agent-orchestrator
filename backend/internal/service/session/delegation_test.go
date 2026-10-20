package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestDelegateTaskSpawnsWorkerAndRefinesTitleThroughBackgroundHarness(t *testing.T) {
	tests := []struct {
		name      string
		agent     domain.AgentHarness
		model     string
		effort    string
		mode      domain.SessionMode
		wantAgent domain.AgentHarness
	}{
		{name: "project default"},
		{name: "requested agent model and mode", agent: domain.HarnessCursor, model: "  sonnet-custom  ", effort: " high ", mode: domain.SessionModeChat, wantAgent: domain.HarnessCursor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var effort *string
			if tt.effort != "" {
				effort = &tt.effort
			}
			st := newFakeStore()
			st.projects["ao"] = domain.ProjectRecord{ID: "ao", Kind: domain.ProjectKindScratch}
			cmd := &fakeCommander{backgroundResult: "Fix renderer"}
			cmd.spawnFunc = func(cfg ports.SpawnConfig) domain.SessionRecord {
				rec := domain.SessionRecord{
					ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness,
					DisplayName: cfg.DisplayName,
				}
				st.sessions[rec.ID] = rec
				return rec
			}
			svc := &Service{store: st, manager: cmd, runBackground: runInline}
			brief := "  Fix the renderer\nwithout changing the API.  "

			out, err := svc.DelegateTask(context.Background(), DelegateTaskInput{
				ProjectID: "ao", Brief: brief, RequestedAgent: tt.agent, Model: tt.model,
				Effort: effort, RequestedMode: tt.mode, TaskPreparation: "prep-token",
			})
			if err != nil {
				t.Fatalf("DelegateTask: %v", err)
			}
			if out.WorkerID != "mer-9" || out.OrchestratorID != "" {
				t.Fatalf("out = %#v, want worker mer-9 only", out)
			}
			if !cmd.spawned || cmd.spawnedCfg.ProjectID != "ao" || cmd.spawnedCfg.Kind != domain.KindWorker || cmd.spawnedCfg.Harness != tt.wantAgent || cmd.spawnedCfg.Prompt != brief || cmd.spawnedCfg.DisplayName != "Fix the renderer without changing the API." {
				t.Fatalf("spawn cfg = %#v", cmd.spawnedCfg)
			}
			if cmd.spawnedCfg.AgentConfig.Model != strings.TrimSpace(tt.model) || cmd.spawnedCfg.AgentConfig.Effort != strings.TrimSpace(tt.effort) {
				t.Fatalf("spawn tuning = %#v", cmd.spawnedCfg.AgentConfig)
			}
			if cmd.spawnedCfg.EffortOverride != (effort != nil) || cmd.spawnedCfg.RequestedMode != tt.mode {
				t.Fatalf("spawn tuning presence/mode = %#v/%q", cmd.spawnedCfg, cmd.spawnedCfg.RequestedMode)
			}
			if len(cmd.backgroundCalls) != 1 {
				t.Fatalf("background calls = %#v, want one", cmd.backgroundCalls)
			}
			if cmd.spawnedCfg.TaskPreparation != "prep-token" {
				t.Fatalf("task preparation = %q", cmd.spawnedCfg.TaskPreparation)
			}
			call := cmd.backgroundCalls[0]
			if call.id != "mer-9" || call.prompt != brief || call.systemPrompt != delegatedTaskTitleSystemPrompt {
				t.Fatalf("background call = %#v", call)
			}
			if len(cmd.sent) != 0 || len(cmd.resumed) != 0 || cmd.spawnCalls != 1 {
				t.Fatalf("title refinement touched a session: sent=%#v resumed=%#v spawns=%d", cmd.sent, cmd.resumed, cmd.spawnCalls)
			}
			if got := st.sessions["mer-9"].DisplayName; got != "Fix renderer" {
				t.Fatalf("display name = %q, want generated title", got)
			}
		})
	}
}

func TestDelegateTaskClientRequestReplaysAndConflictsBeforeSpawn(t *testing.T) {
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker, ClientRequestID: "draft-1", ClientRequestHash: "v1:original", ClientRequestCommitted: true}
	cmd := &fakeCommander{}
	svc := &Service{store: st, manager: cmd}
	input := DelegateTaskInput{ProjectID: "ao", Brief: "Fix it", ClientRequestID: "draft-1", ClientRequestHash: "v1:original"}
	out, err := svc.DelegateTask(context.Background(), input)
	if err != nil || out.WorkerID != "ao-1" || cmd.spawnCalls != 0 {
		t.Fatalf("replay = %+v, spawnCalls=%d, err=%v", out, cmd.spawnCalls, err)
	}
	input.ClientRequestHash = "v1:changed"
	_, err = svc.DelegateTask(context.Background(), input)
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Kind != apierr.KindConflict || apiError.Code != "CLIENT_REQUEST_CONFLICT" || cmd.spawnCalls != 0 {
		t.Fatalf("changed payload: spawnCalls=%d, err=%v", cmd.spawnCalls, err)
	}
	rec := st.sessions["ao-1"]
	rec.ClientRequestCommitted = false
	st.sessions["ao-1"] = rec
	input.ClientRequestHash = "v1:original"
	_, err = svc.DelegateTask(context.Background(), input)
	if !errors.As(err, &apiError) || apiError.Code != "CLIENT_REQUEST_INCOMPLETE" || cmd.spawnCalls != 0 {
		t.Fatalf("incomplete retry: spawnCalls=%d, err=%v", cmd.spawnCalls, err)
	}
}

func TestDelegateTaskNamesGitWorkspaceDuringSpawn(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	cmd := &fakeCommander{}
	_, err := (&Service{store: st, manager: cmd}).DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix login redirect"})
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.spawnedCfg.NameFromPrompt || len(cmd.backgroundCalls) != 0 {
		t.Fatal("Git task must use one provisioning-time naming call")
	}
}

func TestDelegateTaskEmitsSpawnTelemetryOnce(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker, ClientRequestID: "draft-1", ClientRequestHash: "v1", ClientRequestCommitted: true}
	sink := &fakeTelemetrySink{}
	cmd := &fakeCommander{}
	cmd.spawnFunc = func(cfg ports.SpawnConfig) domain.SessionRecord {
		return domain.SessionRecord{ID: "ao-2", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: domain.HarnessCodex}
	}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st, Telemetry: sink})

	if _, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", ClientRequestID: "draft-1", ClientRequestHash: "v1"}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("replay events = %#v, want none", sink.events)
	}
	if _, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", TaskPreparation: "prep-token"}); err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("events = %#v, want one spawned", sink.events)
	}
	ev := sink.events[0]
	if ev.Name != "ao.session.spawned" || ev.SessionID == nil || *ev.SessionID != "ao-2" || ev.Payload["kind"] != "worker" || ev.Payload["harness"] != "codex" {
		t.Fatalf("spawned event = %#v", ev)
	}

	sink.events = nil
	cmd.spawnErr = errors.New("boom")
	if _, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", RequestedAgent: domain.HarnessCodex}); err == nil {
		t.Fatal("DelegateTask should fail")
	}
	if len(sink.events) != 1 || sink.events[0].Name != "ao.session.spawn_failed" || sink.events[0].Payload["harness"] != "codex" {
		t.Fatalf("events = %#v, want one spawn_failed", sink.events)
	}
}

func TestDelegateTaskEmitsFirstSessionOnboarding(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker, IsTaskPreparation: true}
	sink := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st, Telemetry: sink})

	if _, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", TaskPreparation: "ao-1"}); err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if len(sink.events) != 2 || sink.events[0].Name != "ao.session.spawned" || sink.events[1].Name != "ao.onboarding.first_session_spawned" {
		t.Fatalf("events = %#v, want spawned + first_session", sink.events)
	}
}

func TestDelegatedTaskTitles(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{name: "empty provisional", in: " \n\t ", want: "Untitled task"},
		{name: "short provisional", in: "  tell me a joke  ", want: "tell me a joke"},
		{name: "provisional whitespace", in: "Fix the renderer\nwithout changing the API", want: "Fix the renderer without changing the API"},
		{name: "provisional unicode limit", in: strings.Repeat("一", 101), want: strings.Repeat("一", 100)},
		{name: "provisional byte truncation", in: strings.Repeat(" long", 21), want: strings.TrimSpace(strings.Repeat(" long", 21)[:100])},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := delegatedTaskDisplayName(tt.in); got != tt.want {
				t.Fatalf("delegatedTaskDisplayName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	for _, tt := range []struct {
		in   string
		want string
	}{
		{in: "## `Fix renderer.`\nExtra prose", want: "Fix renderer"},
		{in: "Upgrade to C++", want: "Upgrade to C++"},
		{in: "Move to F#", want: "Move to F#"},
		{in: "Fix\x00 renderer", want: "Fix renderer"},
		{in: strings.Repeat("界", 101), want: strings.Repeat("界", 100)},
		{in: " -- ... ", want: ""},
	} {
		if got := generatedTaskTitle(tt.in); got != tt.want {
			t.Errorf("generatedTaskTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDelegateTaskStartsPromptlessWorkerWithoutGeneratingTitle(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Kind: domain.ProjectKindScratch}
	cmd := &fakeCommander{}

	out, err := (&Service{store: st, manager: cmd, runBackground: runInline}).DelegateTask(
		context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: " \n\t "},
	)
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if out.WorkerID != "mer-9" || out.OrchestratorID != "" {
		t.Fatalf("out = %#v, want promptless worker mer-9", out)
	}
	if !cmd.spawned || cmd.spawnedCfg.Prompt != "" || cmd.spawnedCfg.DisplayName != "Untitled task" {
		t.Fatalf("spawn cfg = %#v", cmd.spawnedCfg)
	}
	if len(cmd.backgroundCalls) != 0 || len(cmd.sent) != 0 || len(cmd.resumed) != 0 {
		t.Fatalf("promptless spawn ran title work: background=%#v sent=%#v resumed=%#v", cmd.backgroundCalls, cmd.sent, cmd.resumed)
	}
}

func TestGeneratedTaskTitleDoesNotOverwriteManualRename(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Kind: domain.ProjectKindScratch}
	cmd := &fakeCommander{backgroundResult: "Generated title"}
	cmd.spawnFunc = func(cfg ports.SpawnConfig) domain.SessionRecord {
		rec := domain.SessionRecord{ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, DisplayName: "Mine now"}
		st.sessions[rec.ID] = rec
		return rec
	}

	if _, err := (&Service{store: st, manager: cmd, runBackground: runInline}).DelegateTask(
		context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"},
	); err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if got := st.sessions["mer-9"].DisplayName; got != "Mine now" {
		t.Fatalf("display name = %q, want manual rename preserved", got)
	}
}

func TestDelegateTaskKeepsSpawnSuccessWhenBackgroundTitleFails(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Kind: domain.ProjectKindScratch}
	cmd := &fakeCommander{backgroundErr: errors.New("provider unavailable")}

	out, err := (&Service{store: st, manager: cmd, runBackground: runInline}).DelegateTask(
		context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"},
	)
	if err != nil || out.WorkerID != "mer-9" || !cmd.spawned {
		t.Fatalf("DelegateTask = %#v, %v; want successful worker", out, err)
	}
}

func TestDelegateTaskReturnsBeforeBackgroundTitleCompletes(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Kind: domain.ProjectKindScratch}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	cmd := &fakeCommander{backgroundFunc: func(backgroundTaskCall) (string, error) {
		close(started)
		<-release
		close(finished)
		return "Fix it", nil
	}}
	svc := &Service{store: st, manager: cmd}

	out, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"})
	if err != nil || out.WorkerID != "mer-9" {
		t.Fatalf("DelegateTask = %#v, %v", out, err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background title request did not start")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("background title request did not finish")
	}
}

func TestDelegateTaskSkipsTitleWhenCapacityIsFull(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Kind: domain.ProjectKindScratch}
	cmd := &fakeCommander{}
	slots := make(chan struct{}, 1)
	slots <- struct{}{}

	out, err := (&Service{store: st, manager: cmd, titleRefinementSlots: slots}).DelegateTask(
		context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"},
	)
	if err != nil || out.WorkerID != "mer-9" || len(cmd.backgroundCalls) != 0 {
		t.Fatalf("DelegateTask = %#v, %v; background=%#v", out, err, cmd.backgroundCalls)
	}
}

func TestKillCancelsBackgroundTitle(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Kind: domain.ProjectKindScratch}
	started := make(chan struct{})
	finished := make(chan struct{})
	cmd := &fakeCommander{backgroundFunc: func(call backgroundTaskCall) (string, error) {
		close(started)
		<-call.ctx.Done()
		close(finished)
		return "", call.ctx.Err()
	}}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})

	if _, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"}); err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background title request did not start")
	}
	if _, err := svc.Kill(context.Background(), "mer-9"); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("background title request was not cancelled")
	}
}

func runInline(work func()) { work() }

// ao spawn, including orchestrator-owned workers, uses generic Spawn rather than
// the desktop's DelegateTask route. Both must enable the same naming pipeline.
func TestSpawnNamesImplicitWorkerBranchesFromEveryEntryPoint(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		kind                    domain.ProjectKind
		cfg                     ports.SpawnConfig
		wantName, wantKeepTitle bool
	}{
		{name: "CLI worker", cfg: ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindWorker, Prompt: "Fix login redirect"}, wantName: true},
		{name: "orchestrator Chat worker", cfg: ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindWorker, ParentSessionID: "ao-orch", RequestedMode: domain.SessionModeChat, DisplayName: "Review: login", Prompt: "Review login redirect"}, wantName: true, wantKeepTitle: true},
		{name: "orchestrator TUI worker", cfg: ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindWorker, ParentSessionID: "ao-orch", RequestedMode: domain.SessionModeTUI, Prompt: "Review login redirect"}, wantName: true},
		{name: "workspace project", kind: domain.ProjectKindWorkspace, cfg: ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindWorker, Prompt: "Fix login redirect"}, wantName: true},
		{name: "explicit branch", cfg: ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindWorker, Branch: "review/login", Prompt: "Review login"}},
		{name: "no prompt", cfg: ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindWorker, Prompt: " \n "}},
		{name: "orchestrator", cfg: ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindOrchestrator, Prompt: "Coordinate reviews"}},
		{name: "scratch", kind: domain.ProjectKindScratch, cfg: ports.SpawnConfig{ProjectID: "ao", Kind: domain.KindWorker, Prompt: "Review login"}},
		{name: "standalone", cfg: ports.SpawnConfig{Kind: domain.KindWorker, Harness: domain.HarnessCodex, Prompt: "Review login"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["ao"] = domain.ProjectRecord{ID: "ao", Kind: tt.kind}
			cmd := &fakeCommander{spawnRecord: domain.SessionRecord{ID: "ao-1", ProjectID: tt.cfg.ProjectID, Kind: tt.cfg.Kind, Harness: domain.HarnessCodex}}
			_, _, _, err := (&Service{store: st, manager: cmd}).Spawn(t.Context(), tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if cmd.spawnedCfg.NameFromPrompt != tt.wantName || cmd.spawnedCfg.KeepDisplayName != tt.wantKeepTitle {
				t.Fatalf("naming=%v keepTitle=%v; want %v/%v", cmd.spawnedCfg.NameFromPrompt, cmd.spawnedCfg.KeepDisplayName, tt.wantName, tt.wantKeepTitle)
			}
			if cmd.spawnedCfg.ParentSessionID != tt.cfg.ParentSessionID || cmd.spawnedCfg.Branch != tt.cfg.Branch || cmd.spawnedCfg.DisplayName != tt.cfg.DisplayName {
				t.Fatal("spawn ownership or explicit names changed")
			}
		})
	}
}
