package kimi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The testdata frames are real Kimi Code 2.4.1 screens captured from tmux
// against a stand-in provider: terminal_* through `capture-pane -e` (the
// styled viewport the activity observer reads) at 80 columns, plain_wide_*
// through `capture-pane -p` at 160 columns. Local paths and the session id
// are replaced.
func TestDetectTerminalActivityReadsCapturedKimiFrames(t *testing.T) {
	for _, tc := range []struct {
		frame string
		want  domain.ActivityState
	}{
		{"terminal_idle_start.txt", domain.ActivityIdle},
		{"terminal_idle_after_turn.txt", domain.ActivityIdle},
		// A human's unsent draft does not make the agent busy.
		{"terminal_idle_draft.txt", domain.ActivityIdle},
		// A provider auth error ends the turn and Kimi returns to its prompt.
		{"terminal_auth_error.txt", domain.ActivityIdle},
		{"plain_wide_401.txt", domain.ActivityIdle},
		// Esc ends the turn without a Stop hook.
		{"terminal_after_esc.txt", domain.ActivityIdle},
		{"plain_wide_after_esc.txt", domain.ActivityIdle},
		// The composer stays on screen while a turn runs.
		{"terminal_working.txt", domain.ActivityActive},
		{"plain_wide_working.txt", domain.ActivityActive},
		{"terminal_tool_running.txt", domain.ActivityActive},
		{"plain_wide_tool_running.txt", domain.ActivityActive},
		{"terminal_permission.txt", domain.ActivityWaitingInput},
		{"plain_wide_permission.txt", domain.ActivityWaitingInput},
	} {
		t.Run(tc.frame, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.frame))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := (&Plugin{}).DetectTerminalActivity(string(data))
			if !ok || got != tc.want {
				t.Fatalf("DetectTerminalActivity = (%q, %v), want (%q, true)", got, ok, tc.want)
			}
		})
	}
}

func TestDetectTerminalActivityFailsClosed(t *testing.T) {
	composer := " ╭──────────────╮\n │ >            │\n ╰──────────────╯\n lab-model  ~/project\n          context: 0% (0/128k)\n"
	for name, output := range map[string]string{
		"empty": "",
		// The folder-trust dialog Kimi shows before its first composer.
		"trust dialog": " Trust this folder?\n ↑↓ navigate · Enter select · Esc exit\n  ❯ Trust and continue\n    Exit\n",
		// A box above other content is not the composer at the bottom.
		"composer not at bottom": composer + " ● Done.\n one\n two\n three\n",
		"box without prompt":     " ╭──────────────╮\n │ hello        │\n ╰──────────────╯\n footer\n",
		"open box":               " │ >            │\n ╰──────────────╯\n footer\n",
		// A raw output stream holds partial row redraws, never a whole box.
		"raw redraw": "\x1b[2K\x1b[1G  ⠧ Working… · Tip: @: mention files\x1b[3B\x1b[1G",
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := (&Plugin{}).DetectTerminalActivity(output); ok {
				t.Fatalf("DetectTerminalActivity = (%q, true), want not authoritative", got)
			}
		})
	}
}

func TestDetectTerminalActivityTreatsCompactionAsActive(t *testing.T) {
	output := " ● Done.\n ● Compacting context… · Tip: @: mention files\n" +
		" ╭──────────────╮\n │ >            │\n ╰──────────────╯\n lab-model  ~/project\n          context: 91% (117k/128k)\n"
	if got, ok := (&Plugin{}).DetectTerminalActivity(output); !ok || got != domain.ActivityActive {
		t.Fatalf("DetectTerminalActivity = (%q, %v), want (active, true)", got, ok)
	}
}

// Kimi redraws rows in place, so the observer must read the rendered screen.
func TestTerminalActivityOptionsPreferRenderedScreen(t *testing.T) {
	if !(&Plugin{}).TerminalActivityOptions().PreferStyledOutput {
		t.Fatal("PreferStyledOutput = false, want true")
	}
}
