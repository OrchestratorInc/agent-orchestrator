package stakpak

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Stakpak 0.3.88 draws its composer as a box at the bottom of the screen, with
// one footer row under it:
//
//	┌──────────────────────────────┐
//	│→ █Load test my service …     │   placeholder, or the typed draft
//	└──────────────────────────────┘
//	   $ shell | / commands | ctrl+s shortcuts | profile default | …
//
// The footer is the authoritative state. While a turn runs it starts with a
// spinner and "esc cancel"; while a draft is typed it reads "ctrl+j new line".
// A tool approval replaces the composer with an "Approval Required" box whose
// last row ends in "esc reject all". The side panel (ctrl+y) only appends text
// to the right of these rows.
const (
	composerTop       = "┌"
	composerPrompt    = "│→"
	composerBottom    = "└"
	footerIdle        = "$ shell"
	footerCommands    = "/ commands"
	footerWorking     = "esc cancel"
	footerDraft       = "ctrl+j new line"
	approvalTitle     = "Approval Required"
	approvalRejectAll = "esc reject all"
)

// bottomScan bounds how far up from the last non-blank row the current chrome
// is looked for, so transcript text cannot satisfy a check.
const bottomScan = 12

var (
	_ ports.AgentPromptReadinessProvider       = (*Plugin)(nil)
	_ ports.ContinuousTerminalActivityDetector = (*Plugin)(nil)
	_ ports.TerminalActivityOptionsProvider    = (*Plugin)(nil)
	_ ports.EmptyComposerDetector              = (*Plugin)(nil)
)

// PromptReadinessHints holds the first task until the composer footer is
// drawn. Stakpak takes a few seconds to start, and keys typed before its TUI
// owns the terminal are lost, Enter included. The footer is one style run, so
// it appears verbatim in the raw output stream.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{
		Timeout: 60 * time.Second, PollInterval: 200 * time.Millisecond, Lines: 80,
		Patterns: []string{footerIdle + " | " + footerCommands},
	}, ctx.Err()
}

// ContinuouslyDetectTerminalActivity reports that the screen is the only
// source of every transition.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// RequiresRenderedActivity asks for the rendered screen: Stakpak's full-screen
// redraws never form these rows in the raw output stream.
func (*Plugin) RequiresRenderedActivity() bool { return true }

// TerminalActivityOptions declares the approval box as a native decision.
func (*Plugin) TerminalActivityOptions() ports.TerminalActivityOptions {
	return ports.TerminalActivityOptions{SupportsBlocked: true}
}

// ComposerIsEmpty reports a composer that holds no human draft.
func (*Plugin) ComposerIsEmpty(output string) bool {
	footer, ok := composerFooter(bottomLines(output))
	return ok && strings.Contains(footer, footerIdle) && strings.Contains(footer, footerCommands)
}

// DetectTerminalActivity reads the current chrome. A draft, a popup or an
// unrecognized frame is unknown.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := bottomLines(output)
	if approvalCard(lines) {
		return domain.ActivityBlocked, true
	}
	footer, ok := composerFooter(lines)
	if !ok {
		return "", false
	}
	switch {
	case strings.Contains(footer, footerWorking):
		return domain.ActivityActive, true
	case strings.Contains(footer, footerDraft):
		return "", false
	case strings.Contains(footer, footerIdle) && strings.Contains(footer, footerCommands):
		return domain.ActivityIdle, true
	}
	return "", false
}

// bottomLines returns the last bottomScan non-blank-terminated rows, ANSI
// stripped.
func bottomLines(output string) []string {
	lines := terminalui.PlainTerminalLines(strings.ReplaceAll(output, "\r\n", "\n"))
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > bottomScan {
		lines = lines[len(lines)-bottomScan:]
	}
	return lines
}

// composerFooter finds the composer box closest to the bottom and returns the
// footer row under it. The box must be complete: top border, prompt row, any
// draft continuation rows, bottom border.
func composerFooter(lines []string) (string, bool) {
	for i := len(lines) - 1; i >= 2; i-- {
		if !strings.HasPrefix(strings.TrimLeft(lines[i], " "), composerBottom) {
			continue
		}
		for j := i - 1; j >= 1; j-- {
			row := strings.TrimLeft(lines[j], " ")
			if strings.HasPrefix(row, composerPrompt) {
				if !strings.HasPrefix(strings.TrimLeft(lines[j-1], " "), composerTop) {
					return "", false
				}
				if i+1 >= len(lines) {
					return "", false
				}
				return lines[i+1], true
			}
			if !strings.HasPrefix(row, "│") {
				break
			}
		}
		return "", false
	}
	return "", false
}

// approvalCard reports a pending tool approval: the box title and its
// reject-all hint, with no composer drawn below the box.
func approvalCard(lines []string) bool {
	title := -1
	for i, line := range lines {
		if strings.Contains(line, approvalTitle) && strings.Contains(line, composerTop) {
			title = i
		}
	}
	if title < 0 {
		return false
	}
	hint := false
	for _, line := range lines[title+1:] {
		row := strings.TrimLeft(line, " ")
		if strings.HasPrefix(row, composerPrompt) {
			return false
		}
		if strings.Contains(line, approvalRejectAll) {
			hint = true
		}
	}
	return hint
}
