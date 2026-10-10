package openinterpreter

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestTerminalActivityUsesCurrentNativeChrome(t *testing.T) {
	for _, tc := range []struct {
		name, frame string
		state       domain.ActivityState
		ok          bool
	}{
		{"ready", "› \x1b[2mAsk Open Interpreter to do anything\x1b[0m\n\n  ? for shortcuts                         100% context left", domain.ActivityIdle, true},
		{"running", "• Working (0s • esc to interrupt)\n\n› \n\n  tab to queue message                     90% context left", domain.ActivityActive, true},
		{"wrapped hook status", "Working (0s • esc to interrupt) · running hooks\n› \n  tab to queue message", domain.ActivityActive, true},
		{"native queued inputs", "• Working (0s • esc to interrupt)\n\n• Queued follow-up inputs\n  ↳ Queued follow-up question\n    ⌥+↑ edit last queued message\n\n› Ask Codex to do anything\n\n  ? for shortcuts            100% context left", domain.ActivityActive, true},
		{"native agents navigation", "› \n  ← for agents · ? for shortcuts   100% context left", domain.ActivityIdle, true},
		{"permission", "Would you like to run the following command?\n› 1. Yes\n  2. No\nPress enter to confirm", domain.ActivityWaitingInput, true},
		{"hook trust", "Hooks need review\n› 1. Review hooks\n2. Trust all and continue\n3. Continue without trusting (hooks won't run)\nenter confirm · esc skip", domain.ActivityWaitingInput, true},
		{"question", "Pick an option\n› 1. Choice\n  tab to add notes | enter to submit answer | esc to interrupt", domain.ActivityWaitingInput, true},
		{"auth", "Sign in to Open Interpreter", "", false},
		{"history", "› old task\nAnswer includes ? for shortcuts", "", false},
		{"truncated", "›", "", false},
		{"settled after old status", "• Working (0s • esc to interrupt)\nold result\n› old task\nresult\n› \n  ? for shortcuts                   50% context left", domain.ActivityIdle, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, ok := New().DetectTerminalActivity(tc.frame)
			if state != tc.state || ok != tc.ok {
				t.Fatalf("state = %q, %v; want %q, %v", state, ok, tc.state, tc.ok)
			}
		})
	}
}

func TestComposerDraftAndModalAreNotEmpty(t *testing.T) {
	p := New()
	for _, frame := range []string{"› unfinished human draft\n ? for shortcuts  80% context left", "› 1. Yes\n 2. No\nPress enter to confirm"} {
		if p.ComposerIsEmpty(frame) {
			t.Fatal("draft/modal treated as empty")
		}
	}
	if got := p.InspectTerminalSurface("› \x1b[2mAsk Open Interpreter to do anything\x1b[0m\n ? for shortcuts  80% context left"); got.Composer != ports.TerminalComposerEmpty {
		t.Fatalf("composer = %q", got.Composer)
	}
}
