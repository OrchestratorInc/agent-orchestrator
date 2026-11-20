package sessionmanager

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
)

func TestTerminalForkArgvPerAgent(t *testing.T) {
	for _, tc := range []struct {
		harness domain.AgentHarness
		restore []string
		want    []string
	}{
		{domain.HarnessCodex, []string{"codex", "resume", "-c", "x=1", "--model", "m", "S"}, []string{"codex", "fork", "-c", "x=1", "--model", "m", "S"}},
		{domain.HarnessContinue, []string{"cn", "--auto", "--fork", "S"}, []string{"cn", "--auto", "--fork", "S"}},
		{domain.HarnessOpenCode, []string{"opencode", "--model", "m", "--session", "S"}, []string{"opencode", "--model", "m", "--session", "S", "--fork"}},
		{domain.HarnessPi, []string{"pi", "--model", "m", "--session", "S"}, []string{"pi", "--model", "m", "--fork", "S"}},
		{domain.HarnessGrok, []string{"grok", "--model", "m", "-r", "S"}, []string{"grok", "--model", "m", "--resume", "S", "--fork-session"}},
		{domain.HarnessDroid, []string{"droid", "--auto", "high", "-r", "S"}, []string{"droid", "--auto", "high", "--fork", "S"}},
	} {
		got, ok := terminalForkArgv(tc.harness, tc.restore, "S")
		if !ok || !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, %v; want %v", tc.harness, got, ok, tc.want)
		}
	}
	if _, ok := terminalForkArgv(domain.HarnessAider, []string{"aider"}, "S"); ok {
		t.Error("aider has no fork command")
	}
	if _, ok := terminalForkArgv(domain.HarnessPi, []string{"pi", "--model", "m"}, "S"); ok {
		t.Error("a restore argv without the session id can't be forked")
	}
}

func TestTerminalForkWithoutCommandRefused(t *testing.T) {
	m, _, _, _ := newManager()
	src, err := m.TerminalForkSource(context.Background(), domain.SessionRecord{ID: "mer-0", Harness: domain.HarnessAider, Metadata: domain.SessionMetadata{AgentSessionID: "S"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Refusal != sessionfork.CodeAgentUnsupported {
		t.Fatalf("refusal = %q", src.Refusal)
	}
}

func TestTerminalForkNeedsNativeSession(t *testing.T) {
	m, _, _, _ := newManager()
	src, err := m.TerminalForkSource(context.Background(), domain.SessionRecord{ID: "mer-0", Harness: domain.HarnessCodex}, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Refusal != sessionfork.CodeNativeMissing {
		t.Fatalf("refusal = %q", src.Refusal)
	}
}

func TestTerminalForkAdoptsNewNativeID(t *testing.T) {
	m, st, rt, _ := newManager()
	_ = st
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, RequestedMode: domain.SessionModeTUI,
		Fork: &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc", PrepareTerminal: func(_ context.Context, target ports.ForkTarget) (ports.ForkTerminalLaunch, error) {
			if target.WorkspacePath == "" || target.SessionID == "" {
				t.Errorf("target = %+v", target)
			}
			return ports.ForkTerminalLaunch{Argv: []string{"sh", "-c", "forked"}, AgentSessionID: "child-native"}, nil
		}},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := rt.lastCfg.Argv; !slices.Contains(got, "forked") {
		t.Fatalf("argv = %v", got)
	}
	if rec.Metadata.AgentSessionID != "child-native" {
		t.Fatalf("agent session = %q", rec.Metadata.AgentSessionID)
	}
}

func TestTerminalForkRefusesOlderTurns(t *testing.T) {
	m, _, _, _ := newManager()
	src, err := m.TerminalForkSource(context.Background(), domain.SessionRecord{ID: "mer-0", Harness: domain.HarnessCodex, Metadata: domain.SessionMetadata{AgentSessionID: "S"}}, "turn-2")
	if err != nil || src.Refusal != sessionfork.CodeOlderTurn {
		t.Fatalf("src = %+v, %v", src, err)
	}
}

type launchRecordingAgent struct {
	fakeAgent
	launched *ports.LaunchConfig
}

func (a launchRecordingAgent) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	*a.launched = cfg
	return a.fakeAgent.GetLaunchCommand(ctx, cfg)
}

type launchRecordingAgents struct{ agent launchRecordingAgent }

func (r launchRecordingAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return r.agent, true }

// A text-handoff terminal fork's agent starts with the copied conversation as
// its first message, which is not saved as the session's task.
func TestTerminalTextHandoffForkStartsWithTheNote(t *testing.T) {
	m, _, _, _ := newManager()
	var launched ports.LaunchConfig
	m.agents = launchRecordingAgents{agent: launchRecordingAgent{launched: &launched}}
	var target ports.ForkTarget
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, RequestedMode: domain.SessionModeTUI,
		Fork: &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc", Handoff: func(_ context.Context, got ports.ForkTarget) (string, error) {
			target = got
			return "COPIED-CONVERSATION", nil
		}},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if launched.Prompt != "COPIED-CONVERSATION" {
		t.Fatalf("launch prompt = %q, want the copied conversation", launched.Prompt)
	}
	if target.SessionID != rec.ID || target.WorkspacePath == "" {
		t.Fatalf("note built for %+v, want the fork %s and its worktree", target, rec.ID)
	}
	if rec.Metadata.Prompt != "" || rec.Metadata.LatestUserPrompt != "" {
		t.Fatalf("task = %q, latest = %q; the note must not be saved as the task", rec.Metadata.Prompt, rec.Metadata.LatestUserPrompt)
	}
}

// terminalTurnStore adds the terminal turns the prompt hooks recorded.
type terminalTurnStore struct {
	*fakeStore
	turns map[domain.SessionID][]domain.HandoffTurn
}

func (s terminalTurnStore) ListTerminalTurns(_ context.Context, id domain.SessionID) ([]domain.HandoffTurn, error) {
	return s.turns[id], nil
}

// A terminal fork copies the agent's conversation when it launches, after
// setup. If the original took another turn after the fork captured its files,
// the two would no longer match, so the launch fails and a retry forks again.
func TestTerminalForkRefusesAnOriginalThatMovedOnDuringSetup(t *testing.T) {
	asked := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for name, moveOn := range map[string]func(st terminalTurnStore){
		"unchanged": func(terminalTurnStore) {},
		"busy": func(st terminalTurnStore) {
			rec := st.sessions["mer-0"]
			rec.Activity.State = domain.ActivityActive
			st.sessions["mer-0"] = rec
		},
		"asked again": func(st terminalTurnStore) {
			st.turns["mer-0"] = append(st.turns["mer-0"], domain.HandoffTurn{UserRequest: "and another thing", At: time.Now().Add(time.Second)})
		},
	} {
		m, fake, _, _ := newManager()
		st := terminalTurnStore{fakeStore: fake, turns: map[domain.SessionID][]domain.HandoffTurn{"mer-0": {{UserRequest: "first", At: asked}}}}
		m.store = st
		rec := domain.SessionRecord{ID: "mer-0", ProjectID: "mer", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
			Activity: domain.Activity{State: domain.ActivityIdle}, Metadata: domain.SessionMetadata{AgentSessionID: "S"}}
		fake.sessions["mer-0"] = rec
		src, err := m.TerminalForkSource(context.Background(), rec, "")
		if err != nil || src.PrepareTerminal == nil || src.Freeze == nil {
			t.Fatalf("%s: source = %+v, %v", name, src, err)
		}
		src.Freeze(context.Background())
		moveOn(st)
		_, err = src.PrepareTerminal(context.Background(), ports.ForkTarget{SessionID: "mer-1", WorkspacePath: t.TempDir()})
		if moved := errors.Is(err, ErrForkSourceMovedOn); moved != (name != "unchanged") {
			t.Fatalf("%s: prepare = %v", name, err)
		}
	}
}
