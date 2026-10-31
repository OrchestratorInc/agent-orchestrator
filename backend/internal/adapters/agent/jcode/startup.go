package jcode

import (
	"context"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAfterStartCommand = (*Plugin)(nil)

// startChoiceMarker is jcode's first-run page for a signed-in user. It has no
// timeout and swallows typed input until one of its two choices is taken.
const startChoiceMarker = "How would you like to begin?"

// selectNewSession is Right then (the runtime's) Enter: on the start-choice
// page it takes "New session in <worktree>"; on an ordinary empty composer
// it moves nothing and submits nothing.
const selectNewSession = "\x1b[C"

var (
	sgrPattern          = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	emptyComposerLine   = regexp.MustCompile(`(?m)^[ \t]*\d+>[ \t]*$`)
	onboardingPageLines = regexp.MustCompile(`How would you like to begin\?|Welcome to jcode onboarding`)
)

// AfterStartCommand answers jcode's first-run start choice before AO types the
// task. jcode offers no flag or environment variable to skip it.
func (p *Plugin) AfterStartCommand(ctx context.Context, _ ports.LaunchConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return selectNewSession, nil
}

// AfterStartCommandApplied reports whether the current screen shows jcode's
// empty composer below any onboarding page. A login page (no provider signed
// in) or a running turn is not confirmed, so AO reports a failed start instead
// of typing the task where jcode would drop it.
func (p *Plugin) AfterStartCommandApplied(_ ports.LaunchConfig, output string) bool {
	plain := sgrPattern.ReplaceAllString(output, "")
	composers := emptyComposerLine.FindAllStringIndex(plain, -1)
	if len(composers) == 0 {
		return false
	}
	lastComposer := composers[len(composers)-1][0]
	pages := onboardingPageLines.FindAllStringIndex(plain, -1)
	return len(pages) == 0 || pages[len(pages)-1][0] < lastComposer
}

// readinessPatterns end the startup wait at the empty composer or at the
// start-choice page, which the startup command then answers.
var readinessPatterns = []string{"\n1>\n", "\n1> \n", startChoiceMarker}
