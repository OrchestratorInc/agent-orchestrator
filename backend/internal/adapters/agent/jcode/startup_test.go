package jcode

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Frames captured from jcode v0.99.1 (evidence/onboarding-*.txt).
const (
	startChoiceFrame = "                 Welcome to jcode 🎉\n                How would you like to begin?\n" +
		"   ◖ Find bugs in my most active repo ◗   or   ◖ New session in /tmp/ws ◗\n"
	loginFrame = "                 Welcome to jcode onboarding\n                 Log in to OpenAI?\n                 ◖ Yes ◗   ◖ No ◗\n" +
		"   Esc to skip onboarding (log in later with /login).\n"
	composerFrame = " /tmp/ws (master)\n Ready to use • ✓ Claude Opus 5.5 (default)\n1>\n" +
		"        /tmp/ws  master 3.1k/1.0M ▱▱▱▱▱▱▱▱▱▱ 0% OAuth Claude Opus 5.5 low\n"
	styledComposerFrame = "\x1b[38;5;245m Ready to use\x1b[39m\n\x1b[1m1>\x1b[0m \n\x1b[38;5;102m /tmp/ws 3.1k/1.0M ▱▱▱▱▱▱▱▱▱▱ 0%\x1b[39m\n"
	busyFrame           = "1› fix the bug\n   ✓ bash · Run tests · 13 tok\n2… \n /tmp/ws 22k/1.0M ▱▱▱▱▱▱▱▱▱▱ 2%\n"
)

// The first-run "How would you like to begin?" page has no timeout and eats
// typed input; AO answers it natively with Right, Enter ("New session in
// <worktree>"), which is a no-op on an ordinary empty composer.
func TestStartupCommandAnswersFirstRunChoiceWithANewSession(t *testing.T) {
	p := testPlugin(t)
	hints, err := p.PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !matchesAny(startChoiceFrame, hints.Patterns) {
		t.Fatalf("readiness waits for the full timeout on the start-choice page: %q", hints.Patterns)
	}
	command, err := p.AfterStartCommand(context.Background(), ports.LaunchConfig{})
	if err != nil || command != "\x1b[C" {
		t.Fatalf("startup command = %q, %v", command, err)
	}
}

// The task is typed only once the current screen shows the empty composer;
// a login page or a running turn is never confirmed, so spawn fails with a
// typed error instead of losing the task in onboarding.
func TestStartupCommandConfirmedOnlyByEmptyComposer(t *testing.T) {
	p := testPlugin(t)
	for name, tc := range map[string]struct {
		frame string
		want  bool
	}{
		"composer":              {composerFrame, true},
		"styled":                {styledComposerFrame, true},
		"start-choice":          {startChoiceFrame, false},
		"login":                 {loginFrame, false},
		"busy":                  {busyFrame, false},
		"choice-above-composer": {startChoiceFrame + composerFrame, true},
	} {
		if got := p.AfterStartCommandApplied(ports.LaunchConfig{}, tc.frame); got != tc.want {
			t.Errorf("%s: applied = %v, want %v", name, got, tc.want)
		}
	}
}
