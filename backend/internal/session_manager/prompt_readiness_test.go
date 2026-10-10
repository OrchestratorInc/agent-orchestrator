package sessionmanager

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Tau's cursor styles the first placeholder character separately in PTY output.
const tauStyledComposer = "\x1b[38;2;0;0;0;48;2;216;222;233mA\x1b[0m\x1b[38;2;111;114;117;48;2;16;20;25msk Tau…  Enter submits\x1b[0m"

func TestPromptOutputContainsVisibleReadinessMarker(t *testing.T) {
	for _, tc := range []struct {
		name     string
		output   string
		patterns []string
		want     bool
	}{
		{name: "plain", output: "Ask Tau…  Enter submits", patterns: []string{"Ask Tau…"}, want: true},
		{name: "cursor styling splits marker", output: tauStyledComposer, patterns: []string{"Ask Tau…"}, want: true},
		{name: "later alternative", output: tauStyledComposer, patterns: []string{"Ready...", "Ask Tau…"}, want: true},
		{name: "missing marker", output: "\x1b[32mStarting Tau\x1b[0m", patterns: []string{"Ask Tau…"}},
		{name: "empty output", patterns: []string{"Ask Tau…"}},
		{name: "empty marker", output: tauStyledComposer, patterns: []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptOutputContains(tc.output, tc.patterns); got != tc.want {
				t.Fatalf("promptOutputContains(%q, %q) = %v, want %v", tc.output, tc.patterns, got, tc.want)
			}
		})
	}
}

func TestSpawn_RequiredComposerAcceptsCursorStyledMarker(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{outputs: []string{"booting", tauStyledComposer}}
	msg := &fakeMessenger{}
	m := New(Deps{
		Runtime: rt,
		Agents: singleAgent{agent: readinessAgent{
			afterStartAgent: afterStartAgent{recordingAgent: &recordingAgent{}},
			hints: ports.PromptReadinessHints{
				RequireReady: true,
				Patterns:     []string{"Ask Tau…"},
				PollInterval: time.Millisecond,
				Timeout:      50 * time.Millisecond,
			},
		}},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: msg,
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "fix the button"}); err != nil {
		t.Fatal(err)
	}
	if rt.outputCalls != 2 {
		t.Fatalf("GetOutput calls = %d, want 2", rt.outputCalls)
	}
	if len(msg.msgs) != 1 || msg.msgs[0] != "fix the button" {
		t.Fatalf("delivered prompts = %#v, want one original prompt", msg.msgs)
	}
}
