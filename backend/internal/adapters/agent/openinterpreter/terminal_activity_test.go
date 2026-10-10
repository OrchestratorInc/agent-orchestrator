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
		{"released status footer", "\x1b[1m›\x1b[22m \x1b[2mAsk Codex to do anything\x1b[0m\n\n gpt-5.1-codex default · /tmp/workspace    ⚠ 3 warnings · f2 to view\n\n\n", domain.ActivityIdle, true},
		{"released active status footer", "• Working (0s • esc to interrupt)\n\n\n\x1b[1m›\x1b[22m \n\n gpt-5.1-codex default · /tmp/workspace", domain.ActivityActive, true},
		{"plain transcript with dots", "› old task\nresult · /tmp/workspace", "", false},
		{"dim transcript marker", "\x1b[1;2m› old task\x1b[0m\nresult · /tmp/workspace", "", false},
		{"permission", "Would you like to run the following command?\n› 1. Yes\n  2. No\nPress enter to confirm", domain.ActivityWaitingInput, true},
		{"hook trust", "Hooks need review\n› 1. Review hooks\n2. Trust all and continue\n3. Continue without trusting (hooks won't run)\nenter confirm · esc skip", domain.ActivityWaitingInput, true},
		{"native model picker", "Choose a model\n\x1b[1m›\x1b[22m 1. model\n  tab / ↑ ↓ move · enter select · esc close", domain.ActivityWaitingInput, true},
		{"native hooks browser", "Hooks\n  SessionStart\n  UserPromptSubmit\n\x1b[1m›\x1b[22m Interrupt\n\n  enter details · esc close", domain.ActivityWaitingInput, true},
		{"native keymap picker", "Keymap\n  All  Chat  App\n\x1b[1m›\x1b[22m Action\n\n  left/right group · enter edit · esc close", domain.ActivityWaitingInput, true},
		{"native status settings preview", "Configure Status Line\n\x1b[1m›\x1b[22m [x] Use theme colors\n  [x] model\n  [x] current-dir\n\n  gpt-5-codex · ~/codex-rs · jif/statusline-preview\n  space toggle · ←/→ reorder · enter save · esc cancel", domain.ActivityWaitingInput, true},
		{"native custom prompt", "Custom prompt\n\x1b[1m›\x1b[22m prompt text\n\n  enter submit · esc back", domain.ActivityWaitingInput, true},
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
