package stakpak

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Frames captured from Stakpak 0.3.88 at 120x40 in tmux, trimmed to the rows
// the detector reads.
const (
	frameIdle = ` ┃ Create a file named hello.txt containing the word hi
 ✓ Created hello.txt with hi


┌──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│→ █Load test my service to right-size the resources needed                                                            │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
                                          $ shell | / commands | ctrl+s shortcuts | profile default | ctrl+y side panel
`
	frameDraft = `┌──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│→ Run the shell command: sleep 20 && echo done > out.txt█                                                             │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
 ctrl+j new line | @ files
`
	frameWorking = ` ╭─● Run Command - Running for 5s . ctrl+r to re-run in shell mode ───────────────────────────────────────────────────╮
 │ $ sleep 20 && echo done > out.txt                                                                                  │
 ╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯


┌──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│→ █                                                                                                                   │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
 ▄▀ Stakpaking... - esc cancel            $ shell | / commands | ctrl+s shortcuts | profile default | ctrl+y side panel
`
	frameApproval = ` ╭ Create ────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
 │ 1/1 hello.txt +1 -0                                                                                                │
 │         1  + hi                                                                                                    │
 ╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯


 ┌─ Approval Required ────────────────────────────────────────────────────────────────────────────────────────────────┐
 │  ✓ Create                                                                                                          │
 │ space toggle  ←→ navigate  enter submit  esc reject all                                                            │
 └────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
`
	frameSidePanelIdle = `                                                                                        │  Session 2349...ff6a  ctrl+x
                                                                                        │
                                                                                        │  v0.3.88
┌─────────────────────────────────────────────────────────────────────────────────────┐ │
│→ █                                                                                  │ │  tab select  enter toggle
└─────────────────────────────────────────────────────────────────────────────────────┘ │  ctrl+y hide  ctrl+g changes
                                               $ shell | / commands | ctrl+s shortcuts  │
`
	// A reply quoting the footer text must not read as current chrome.
	frameTranscriptOnly = ` ┃ what does the footer say?
 It says "$ shell | / commands" and "esc cancel" while working.
`
)

func TestDetectTerminalActivity(t *testing.T) {
	p := New()
	for _, tc := range []struct {
		name  string
		frame string
		want  domain.ActivityState
		ok    bool
	}{
		{"idle placeholder", frameIdle, domain.ActivityIdle, true},
		{"draft", frameDraft, "", false},
		{"working", frameWorking, domain.ActivityActive, true},
		{"approval", frameApproval, domain.ActivityBlocked, true},
		{"side panel idle", frameSidePanelIdle, domain.ActivityIdle, true},
		{"transcript only", frameTranscriptOnly, "", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := p.DetectTerminalActivity(tc.frame)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("DetectTerminalActivity = (%q, %t), want (%q, %t)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDetectTerminalActivityStripsANSI(t *testing.T) {
	styled := "\x1b[38;2;80;80;80m┌" + strings.Repeat("─", 20) + "┐\n" +
		"│\x1b[38;5;8m→ \x1b[38;5;6m\x1b[48;5;6m█\x1b[38;5;8m\x1b[49mLoad test my service\x1b[39m   \x1b[38;2;80;80;80m│\n" +
		"└" + strings.Repeat("─", 20) + "┘\n" +
		"\x1b[39m    \x1b[38;2;80;80;80m$ shell | / commands | ctrl+s shortcuts | profile \x1b[39mdefault\n"
	if got, ok := New().DetectTerminalActivity(styled); got != domain.ActivityIdle || !ok {
		t.Fatalf("styled idle frame = (%q, %t), want idle", got, ok)
	}
}

func TestComposerIsEmpty(t *testing.T) {
	p := New()
	if !p.ComposerIsEmpty(frameIdle) || !p.ComposerIsEmpty(frameWorking) {
		t.Fatal("placeholder composer must read as empty")
	}
	if p.ComposerIsEmpty(frameDraft) || p.ComposerIsEmpty(frameApproval) || p.ComposerIsEmpty(frameTranscriptOnly) {
		t.Fatal("a draft, an approval or a missing composer must not read as empty")
	}
}

func TestPromptReadinessWaitsForTheComposerFooter(t *testing.T) {
	hints, err := New().PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil || hints.Timeout <= 0 || len(hints.Patterns) != 1 {
		t.Fatalf("hints = %+v, %v", hints, err)
	}
	if !strings.Contains(frameIdle, hints.Patterns[0]) || strings.Contains(frameApproval, hints.Patterns[0]) {
		t.Fatalf("pattern %q does not identify the idle composer footer", hints.Patterns[0])
	}
}
