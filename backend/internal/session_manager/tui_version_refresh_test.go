package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/gemini"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/grok"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/pi"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type viewerGate struct {
	onScreen bool
}

func (g *viewerGate) BeginInputDrain(string) (time.Time, func()) { return time.Time{}, func() {} }
func (g *viewerGate) TerminalOnScreen(string) bool               { return g.onScreen }

func TestTerminalRestartWaitsForEveryBlocker(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	mgr, _, _ := newChatManager(&recordingLauncher{})
	mgr.clock = func() time.Time { return now }
	mgr.agents = singleAgent{agent: claudecode.New()}
	gate := &viewerGate{}
	mgr.SetTerminalInputGate(gate)
	base := domain.SessionRecord{
		ID: "mer-1", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity:    domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Hour)},
		Metadata:    domain.SessionMetadata{RuntimeHandleID: "mer-1", RuntimeLaunchID: "launch-1"},
		StaleBinary: &domain.SessionStaleBinary{Version: "2.1.300", Since: now.Add(-time.Hour)},
	}
	if !mgr.terminalRestartEligible(base, 10*time.Minute) {
		t.Fatal("an idle, unwatched, stale terminal was not eligible")
	}
	failed := now
	for name, change := range map[string]func(*domain.SessionRecord){
		"current release": func(r *domain.SessionRecord) { r.StaleBinary = nil },
		"failed switch": func(r *domain.SessionRecord) {
			r.StaleBinary = &domain.SessionStaleBinary{Version: "2.1.300", SwitchFailedAt: &failed}
		},
		"working":          func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityActive },
		"pending approval": func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityBlocked },
		"recently active":  func(r *domain.SessionRecord) { r.Activity.LastActivityAt = now.Add(-time.Minute) },

		"orchestrator":          func(r *domain.SessionRecord) { r.Kind = domain.KindOrchestrator },
		"chat":                  func(r *domain.SessionRecord) { r.Mode = domain.SessionModeChat },
		"terminated":            func(r *domain.SessionRecord) { r.IsTerminated = true },
		"no terminal to resume": func(r *domain.SessionRecord) { r.Metadata.RuntimeHandleID = "" },
	} {
		rec := base
		change(&rec)
		if mgr.terminalRestartEligible(rec, 10*time.Minute) {
			t.Fatalf("%s: restart was allowed", name)
		}
	}
	gate.onScreen = true
	if mgr.terminalRestartEligible(base, 10*time.Minute) {
		t.Fatal("restarted a terminal shown on screen")
	}
}

type styledScreenRuntime struct {
	*fakeRuntime
	screen string
}

func (r *styledScreenRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return r.screen, nil
}

// A viewer leaving sends a focus change into the terminal after the idle
// report, and no new idle report follows. The rendered screen decides then.
func TestTerminalRestartAfterInputThatChangedNothing(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	mgr, _, rt := newChatManager(&recordingLauncher{})
	mgr.clock = func() time.Time { return now }
	mgr.agents = singleAgent{agent: transitionSurfaceAgent{}}
	screen := &styledScreenRuntime{fakeRuntime: rt}
	mgr.runtime = screen
	rec := domain.SessionRecord{
		ID: "mer-1", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Hour)},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "mer-1", RuntimeLaunchID: "launch-1"},
	}
	ctx := context.Background()

	if !mgr.terminalIdleForRestart(ctx, rec, now.Add(-2*time.Hour)) {
		t.Fatal("an idle report newer than the last input was not enough")
	}
	focusChange := now.Add(-30 * time.Minute)
	for _, test := range []struct {
		screen string
		want   bool
	}{
		{screen: idleTerminalOutput, want: true},
		{screen: draftTerminalOutput},
		{screen: activeTerminalOutput},
		{screen: ambiguousTerminalOutput},
	} {
		screen.screen = test.screen
		if got := mgr.terminalIdleForRestart(ctx, rec, focusChange); got != test.want {
			t.Errorf("screen %q: restart = %v, want %v", test.screen, got, test.want)
		}
	}
	screen.screen = idleTerminalOutput
	if mgr.terminalIdleForRestart(ctx, rec, now.Add(-100*time.Millisecond)) {
		t.Fatal("restarted before the latest input settled")
	}
}

func TestTerminalRefreshCapabilities(t *testing.T) {
	for harness, agent := range map[domain.AgentHarness]ports.Agent{
		domain.HarnessClaudeCode: claudecode.New(), domain.HarnessCodex: codex.New(),
		domain.HarnessOpenCode: opencode.New(), domain.HarnessPi: pi.New(), domain.HarnessGrok: grok.New(),
		domain.HarnessCursor: cursor.New(), domain.HarnessGemini: gemini.New(),
	} {
		t.Run(string(harness), func(t *testing.T) {
			mgr, _, _ := newChatManager(&recordingLauncher{})
			mgr.agents = singleAgent{agent: agent}
			mgr.SetTerminalInputGate(&viewerGate{})
			rec := domain.SessionRecord{Harness: harness, Kind: domain.KindWorker,
				Activity:    domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-time.Hour)},
				Metadata:    domain.SessionMetadata{RuntimeHandleID: "pane", RuntimeLaunchID: "launch", AgentSessionID: "native-original", AgentSessionIDLaunchID: "launch"},
				StaleBinary: &domain.SessionStaleBinary{Version: "new"}}
			if !mgr.terminalRestartEligible(rec, time.Minute) {
				t.Fatal("native-resumable harness excluded")
			}
			// Capabilities belong to adapters, never to a recognized name alone.
			mgr.agents = singleAgent{agent: fakeAgent{}}
			if mgr.terminalRestartEligible(rec, time.Minute) {
				t.Fatal("adapter without a refresh capability was accepted")
			}
		})
	}
}

func TestTerminalRefreshIdentityRequiresCurrentLaunch(t *testing.T) {
	rec := domain.SessionRecord{Metadata: domain.SessionMetadata{RuntimeLaunchID: "running", AgentSessionID: "original", AgentSessionIDLaunchID: "running"}}
	if !terminalRefreshIdentityVerified(rec) {
		t.Fatal("current native identity rejected")
	}
	for _, mutate := range []func(*domain.SessionRecord){
		func(r *domain.SessionRecord) { r.Metadata.AgentSessionID = " " },
		func(r *domain.SessionRecord) { r.Metadata.RuntimeLaunchID = "" },
		func(r *domain.SessionRecord) { r.Metadata.AgentSessionIDLaunchID = "previous" },
	} {
		current := rec
		mutate(&current)
		if terminalRefreshIdentityVerified(current) {
			t.Fatal("unverified native identity accepted")
		}
	}
}

func TestVersionRefreshDoesNotCommitFailedStartup(t *testing.T) {
	rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}, fencedResult: ports.FencedProbeResult{Liveness: ports.FencedDead}}}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	mgr, store, _ := newExitedResumeManager(t, rt, agent)
	mgr.launchStartWait = time.Millisecond
	rec := store.sessions["mer-1"]
	rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now()}
	store.sessions[rec.ID] = rec
	_, err := mgr.resumeAgentRecordWithPolicy(context.Background(), "version refresh", rec, true)
	if err == nil {
		t.Fatal("failed startup reported as migrated")
	}
	got := store.sessions[rec.ID]
	if got.Metadata.RuntimeLaunchID != "launch-old" || got.Metadata.AgentSessionID != "agent-x" || got.StaleBinary == nil || got.IsTerminated {
		t.Fatalf("failed migration lost recoverable state: %+v", got)
	}
}

type refreshTestAgent struct{ supervisedLaunchAgent }

func (refreshTestAgent) SupportsTerminalVersionRefresh() bool { return true }

type refreshTestRuntime struct {
	*fakeRestartRuntime
	commands int
}

func (r *refreshTestRuntime) RunningWorkloadCommands(context.Context, ports.RuntimeHandle, ports.SupervisedProcessRef) (int, error) {
	return r.commands, nil
}

type refreshTestStore struct {
	*fakeStore
	failed int
}

func (s *refreshTestStore) ListVersionRefreshTerminalCandidates(context.Context, time.Time) ([]domain.SessionID, error) {
	return []domain.SessionID{"mer-1"}, nil
}
func (s *refreshTestStore) SetSessionVersionSwitchFailed(_ context.Context, id domain.SessionID, version string, at time.Time) error {
	rec := s.sessions[id]
	rec.StaleBinary = &domain.SessionStaleBinary{Version: version, Since: at, SwitchFailedAt: &at}
	s.sessions[id] = rec
	s.failed++
	return nil
}

type refreshInputGate struct {
	lastInput time.Time
	releases  int
}

func (g *refreshInputGate) BeginInputDrain(string) (time.Time, func()) {
	return g.lastInput, func() { g.releases++ }
}
func (*refreshInputGate) TerminalOnScreen(string) bool { return false }

func TestRefreshStaleTerminalWithoutChatHandoff(t *testing.T) {
	for _, scenario := range []string{"success", "active", "typing", "workload", "unverified identity", "restart failure", "startup failure", "delayed startup failure", "history missing", "history probe failure", "unknown startup probe"} {
		t.Run(scenario, func(t *testing.T) {
			rt := &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}}}
			agent := refreshTestAgent{supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}}
			mgr, st, _ := newExitedResumeManager(t, rt, agent)
			mgr.launchStartWait = time.Millisecond
			store := &refreshTestStore{fakeStore: st}
			mgr.store = store
			rec := st.sessions["mer-1"]
			rec.Harness = domain.HarnessPi
			rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-time.Hour)}
			rec.Metadata.AgentSessionIDLaunchID = rec.Metadata.RuntimeLaunchID
			rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now().Add(-time.Hour)}
			gate := &refreshInputGate{}
			mgr.SetTerminalInputGate(gate)
			switch scenario {
			case "active":
				rec.Activity.State = domain.ActivityActive
			case "typing":
				gate.lastInput = time.Now()
			case "workload":
				rt.commands = 1
			case "unverified identity":
				rec.Metadata.AgentSessionIDLaunchID = "other"
			case "restart failure":
				rt.restartErr = errors.New("cannot restart")
			case "unknown startup probe":
				rt.fencedResult = ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
			case "delayed startup failure":
				rt.supervisedSequence = []bool{true, false}
			case "history missing":
				mgr.agents = singleAgent{agent: refreshHistoryAgent{refreshTestAgent: agent}}
			case "history probe failure":
				mgr.agents = singleAgent{agent: refreshHistoryAgent{refreshTestAgent: agent, err: errors.New("history unreadable")}}
			case "startup failure":
				rt.fencedResult = ports.FencedProbeResult{Liveness: ports.FencedDead}
			}
			st.sessions[rec.ID] = rec
			err := mgr.RefreshStaleTerminals(context.Background(), 10*time.Minute)
			wantFailure := scenario == "restart failure" || scenario == "startup failure" || scenario == "delayed startup failure" || scenario == "history missing" || scenario == "history probe failure" || scenario == "unknown startup probe"
			if (err != nil) != wantFailure {
				t.Fatalf("refresh error = %v", err)
			}
			wantRestarts := 0
			if scenario == "success" || (wantFailure && scenario != "history missing" && scenario != "history probe failure") {
				wantRestarts = 1
			}
			if rt.restarted != wantRestarts {
				t.Fatalf("restarts = %d, want %d", rt.restarted, wantRestarts)
			}
			if scenario != "active" && gate.releases != 1 {
				t.Fatalf("input gate released %d times", gate.releases)
			}
			got := st.sessions[rec.ID]
			if got.IsTerminated || got.Metadata.AgentSessionID != "agent-x" || got.Metadata.WorkspacePath != rec.Metadata.WorkspacePath {
				t.Fatalf("lost recoverable conversation: %+v", got)
			}
			if scenario == "success" {
				if got.Metadata.RuntimeLaunchID != "launch-new" || got.Metadata.AgentSessionIDLaunchID != "launch-new" {
					t.Fatalf("new launch not bound to original conversation: %+v", got.Metadata)
				}
				if rt.lastCfg.WorkspacePath != rec.Metadata.WorkspacePath {
					t.Fatal("working directory changed")
				}
			} else if got.Metadata.RuntimeLaunchID != "launch-old" {
				t.Fatal("unsuccessful migration committed a new launch")
			}
			if wantFailure && (store.failed != 1 || got.StaleBinary == nil || got.StaleBinary.SwitchFailedAt == nil) {
				t.Fatal("failure was not retained")
			}
			if scenario == "unknown startup probe" {
				if got.Activity.State != domain.ActivityIdle {
					t.Fatal("unknown startup was treated as a dead process")
				}
				if err := mgr.RetryVersionMoves(context.Background()); err != nil {
					t.Fatal(err)
				}
				if len(mgr.versionRetries.pending()) != 1 || rt.restarted != 1 {
					t.Fatal("unknown child was replaced or its retry intent discarded")
				}
			}
		})
	}
}

func TestFailedVersionMoveRetryNeverStartsFreshConversation(t *testing.T) {
	rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}}
	mgr, st, _ := newExitedResumeManager(t, rt, fakeAgent{})
	rec := st.sessions["mer-1"]
	failed := time.Now()
	rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", SwitchFailedAt: &failed}
	rec.Metadata.AgentSessionID = ""
	st.sessions[rec.ID] = rec
	mgr.versionRetries.note(rec.ID)
	if err := mgr.RetryVersionMoves(context.Background()); err == nil {
		t.Fatal("retry silently started a fresh conversation")
	}
	if rt.restarted != 0 || rt.created != 0 {
		t.Fatal("fresh process started")
	}
	if st.sessions[rec.ID].StaleBinary == nil {
		t.Fatal("failed migration notice lost")
	}
}

func TestFailedTerminalChildStartupCanRetryWithoutOldExitCallback(t *testing.T) {
	dead := false
	rt := &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{
		aliveByHandle: map[string]bool{"tmux-mer-1": true}, supervisedSequence: []bool{true, false}, supervisedAliveOverride: &dead,
	}}}
	agent := refreshTestAgent{supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}}
	mgr, st, _ := newExitedResumeManager(t, rt, agent)
	store := &refreshTestStore{fakeStore: st}
	mgr.store = store
	mgr.SetTerminalInputGate(&refreshInputGate{})
	mgr.launchStartWait = time.Millisecond
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessPi
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-time.Hour)}
	rec.Metadata.AgentSessionIDLaunchID = rec.Metadata.RuntimeLaunchID
	rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now().Add(-time.Hour)}
	st.sessions[rec.ID] = rec
	if err := mgr.RefreshStaleTerminals(context.Background(), time.Minute); err == nil {
		t.Fatal("replacement child startup failure was accepted")
	}
	failed := st.sessions[rec.ID]
	if failed.Metadata.RuntimeLaunchID != "launch-old" || failed.Metadata.AgentSessionID != "agent-x" || failed.StaleBinary == nil {
		t.Fatalf("recoverable native state lost: %+v", failed)
	}
	// No old-generation exit hook is delivered. The proven-dead replacement
	// must still leave a resumable stopped state and an actionable retry.
	alive := true
	rt.supervisedAliveOverride = &alive
	if err := mgr.RetryVersionMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.restarted != 2 || st.sessions[rec.ID].Metadata.RuntimeLaunchID != "launch-new" {
		t.Fatalf("failed child was not retried: restarts=%d state=%+v", rt.restarted, st.sessions[rec.ID])
	}
}

type refreshHistoryAgent struct {
	refreshTestAgent
	err error
}

func (a refreshHistoryAgent) NativeConversationExists(context.Context, ports.SessionRef, string, map[string]string) (bool, error) {
	return false, a.err
}

func TestRefreshCapableHarnessLaunchesAreSupervised(t *testing.T) {
	for _, harness := range agentregistry.Harnessed() {
		agent := harness.Agent
		capability, ok := agent.(ports.AgentTerminalVersionRefresher)
		if !ok || !capability.SupportsTerminalVersionRefresh() {
			continue
		}
		name := string(harness.Harness)
		t.Run(name, func(t *testing.T) {
			mgr, _, _ := newExitedResumeManager(t, &fakeRuntime{}, agent)
			env := map[string]string{}
			argv, launchID, err := mgr.superviseAgentProcess(agent, "mer-1", env, []string{name, "--native-resume"})
			if err != nil {
				t.Fatal(err)
			}
			if len(argv) < 3 || argv[1] != "agent-process" || env[EnvSupervisedProcess] != "1" || launchID == "" {
				t.Fatalf("refresh-capable launch has no exact workload/liveness proof: argv=%q env=%v", argv, env)
			}
		})
	}
}

func TestTerminalRefreshSupportMatrix(t *testing.T) {
	supported := map[string]bool{}
	for _, harness := range domain.AllHarnesses {
		supported[string(harness)] = true
	}
	for _, name := range []string{"aider", "continue", "deepseek-harness", "memcode", "unreal-agent"} {
		supported[name] = false
	}
	for _, adapter := range agentregistry.Constructors() {
		name := adapter.Manifest().ID
		capability, ok := adapter.(ports.AgentTerminalVersionRefresher)
		got := ok && capability.SupportsTerminalVersionRefresh()
		if got != supported[name] {
			t.Errorf("%s refresh capability = %t, want %t", name, got, supported[name])
		}
		delete(supported, name)
	}
	if len(supported) != 0 {
		t.Fatalf("supported harnesses missing from registry: %v", supported)
	}
}
