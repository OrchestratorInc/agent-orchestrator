package claudecode

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// claudeAbortedTurnScreen reproduces the live pane of a Claude Code session
// whose turn died on an expired OAuth login: the turn's Stop hook never fired,
// the composer holds an unsent user draft, and the CLI sits idle at the
// prompt. The base fixture is plain; styled cases preserve the same visible
// text while adding terminal formatting around the login prompt.
func claudeAbortedTurnScreen(draft string) string {
	rule := strings.Repeat("─", 48)
	return "⏺ Stopped watching Artifact: \"scm-observer.md\" (connection lost)\n" +
		"\n" +
		"⏺ Login expired · Please run /login\n" +
		"\n" +
		"✻ Worked for 0s\n" +
		"\n" +
		rule + "\n" +
		draft +
		rule + "\n" +
		"\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · PR #4090\n" +
		"  ⧉  scm-observer"
}

func TestDetectTerminalActivityWaitsForLoginAfterAbortedTurn(t *testing.T) {
	plugin := &Plugin{}
	tests := []struct {
		name   string
		output string
	}{
		{
			name: "login expired with staged draft",
			output: claudeAbortedTurnScreen("❯ so btw, the status isn't permanently stuck at \"Checking merge readiness\". It's stuck at that for\n" +
				"  most of the time but keeps showing the merge status once in a while.\n"),
		},
		{
			name:   "login expired with empty composer",
			output: claudeAbortedTurnScreen("❯\n"),
		},
		{
			name: "styled login prompt with staged draft",
			output: strings.Replace(claudeAbortedTurnScreen("❯ continue\n"),
				"Login expired · Please run /login", "Login \x1b[31mexpired\x1b[0m · Please run \x1b[1m/login\x1b[0m", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, ok := plugin.DetectTerminalActivity(tt.output)
			if !ok || state != domain.ActivityWaitingInput {
				t.Fatalf("DetectTerminalActivity = (%q, %v), want (waiting_input, true)", state, ok)
			}
		})
	}
}

func TestDetectTerminalActivityFailsClosedOffIdle(t *testing.T) {
	plugin := &Plugin{}
	rule := strings.Repeat("─", 48)
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "active spinner row",
			output: "✶ Generating… (esc to interrupt · 2s)\n" + rule + "\n❯\n" + rule,
		},
		{
			name: "active with interrupt hint in footer",
			output: "✻ Computing… (24s · ↓ 114 tokens)\n" + rule + "\n❯\n" + rule +
				"\n⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt · ← for agents",
		},
		{
			name:   "permission dialog",
			output: "Do you want to proceed?\n❯ 1. Yes\n  2. No\nPress enter to confirm",
		},
		{
			name:   "no recognizable surface",
			output: "plain build output\nwithout any provider chrome\n",
		},
		{
			name:   "empty capture",
			output: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, ok := plugin.DetectTerminalActivity(tt.output)
			if ok {
				t.Fatalf("DetectTerminalActivity = (%q, true), want not authoritative", state)
			}
		})
	}
}
