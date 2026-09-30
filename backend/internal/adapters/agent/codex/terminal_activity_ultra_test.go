package codex

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestInspectTerminalSurfaceUltraPrompt(t *testing.T) {
	const footer = "\n\ngpt-5.4 ultra · fixture\n"
	const prompt = "\x1b[1;35m»\x1b[0m "
	const placeholder = "\x1b[2mAsk Codex to do anything\x1b[0m"
	for _, tc := range []struct {
		name, frame string
		work        ports.TerminalSurfaceWorkState
		composer    ports.TerminalComposerState
	}{
		{"idle-placeholder", prompt + placeholder + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		{"idle-blank", prompt + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		{"hidden-footer", "• Ready\n\n" + prompt + placeholder, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		{"typed-placeholder-text", prompt + "Ask Codex to do anything" + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerDraft},
		{"typed-marker-text", prompt + "› and » are draft text" + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerDraft},
		{"wrapped-draft", prompt + "\n\nkeep this draft" + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerDraft},
		{"single-draft-wrapped-ultra-marker", "\x1b[1m›\x1b[0m keep this draft\n  »" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"ultra-draft-wrapped-single-marker", prompt + "keep this draft\n  ›" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"single-blank-wrapped-ultra-marker", "\x1b[1m›\x1b[0m\n  »" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"ultra-blank-wrapped-single-marker", prompt + "\n  ›" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"single-draft-wrapped-single-marker", "\x1b[1m›\x1b[0m keep this draft\n  ›" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"ultra-draft-wrapped-ultra-marker", prompt + "keep this draft\n  »" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"single-wrapped-without-footer", "• Ready\n\x1b[1m›\x1b[0m keep this draft\n  »", ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"ultra-wrapped-without-footer", "• Ready\n" + prompt + "keep this draft\n  ›", ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"wrapped-footer-shaped-draft", prompt + "keep this draft\n  contains · separator\n  ›" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"dim-wrapped-marker-is-ambiguous", prompt + "keep this draft\n  \x1b[2m›\x1b[0m" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		{"styled-history-current-ultra", "\x1b[1m›\x1b[0m old prompt\n• Ready\n" + prompt + placeholder + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		{"styled-history-current-single", prompt + "old prompt\n• Ready\n\x1b[1m›\x1b[0m " + placeholder + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		{"inherited-bold-current-marker", "\x1b[1m• Ready\n»\x1b[0m " + placeholder + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		{"active", "• Working (4s • esc to interrupt)\n" + prompt + placeholder + footer, ports.TerminalSurfaceWorkActive, ports.TerminalComposerEmpty},
		{"plain-transcript", "Example:\n»\n", ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerEmpty},
		{"dim-transcript", "Example:\n\x1b[2m»\x1b[0m\n", ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerEmpty},
		{"single-history-ultra-draft", "› prior prompt\n• Ready\n" + prompt + "keep draft" + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerDraft},
		{"ultra-history-single-draft", "» prior prompt\n• Ready\n\x1b[1m›\x1b[0m keep draft" + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerDraft},
		{"approval-still-pending", "› 1. Approve once\n  2. Deny\nPress enter to confirm or esc to go back\n" + footer, ports.TerminalSurfaceWorkWaitingInput, ports.TerminalComposerUnknown},
		{"completed-approval-ultra-idle", "› 1. Approve once\n  2. Deny\nPress enter to confirm or esc to go back\n" + prompt + placeholder + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := New().InspectTerminalSurface(tc.frame)
			if got.Work != tc.work || got.Composer != tc.composer {
				t.Fatalf("work=%v composer=%v; want work=%v composer=%v", got.Work, got.Composer, tc.work, tc.composer)
			}
		})
	}
}

func TestInspectTerminalSurfacePlainPromptAmbiguity(t *testing.T) {
	const footer = "\n\ngpt-5.4 high · fixture\n"
	for _, marker := range []string{"›", "»"} {
		for _, tc := range []struct {
			name, frame string
			work        ports.TerminalSurfaceWorkState
			composer    ports.TerminalComposerState
		}{
			{"plain-idle", marker + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
			{"plain-marker-dim-placeholder", marker + " \x1b[2mWrite tests\x1b[0m" + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
			{"plain-draft", marker + " keep this draft" + footer, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerDraft},
			{"wrapped-single", marker + " keep this draft\n›" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
			{"wrapped-ultra", marker + " keep this draft\n»" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
			{"blank-then-wrapped-single", marker + "\n  ›" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
			{"blank-then-wrapped-ultra", marker + "\n  »" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
			{"wrapped-without-footer", marker + " keep this draft\n»", ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
			{"footer-like-draft-content", marker + " keep this draft\ncontains · separator\n»" + footer, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		} {
			t.Run(marker+"/"+tc.name, func(t *testing.T) {
				got := New().InspectTerminalSurface(tc.frame)
				if got.Work != tc.work || got.Composer != tc.composer {
					t.Fatalf("work=%v composer=%v; want work=%v composer=%v", got.Work, got.Composer, tc.work, tc.composer)
				}
			})
		}
	}
	t.Run("completed-picker-without-styled-origin", func(t *testing.T) {
		frame := "Run this command?\n› 1. Approve once\n  2. Deny\nPress enter to confirm or esc to go back\n› \x1b[2mAdd tests\x1b[0m" + footer
		got := New().InspectTerminalSurface(frame)
		if got.Work != ports.TerminalSurfaceWorkUnknown || got.Composer != ports.TerminalComposerUnknown {
			t.Fatalf("two unstyled prompt rows cannot prove the current origin: %+v", got)
		}
	})
}
