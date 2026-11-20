package codebuddy

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DeriveActivityState maps supported native hook events to activity.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit", "pre-tool-use", "post-tool-use", "post-tool-use-failure":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityWaitingInput, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		// SessionStart proves native identity, not initialized input readiness.
		// Conversation-end is not proof that the native process exited.
		return "", false
	}
}

// ContinuouslyDetectTerminalActivity enables current viewport observation alongside hooks.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// ValidateTerminalMessage requires an initialized empty native composer before pasting.
func (p *Plugin) ValidateTerminalMessage(output, message string) error {
	if err := validateTask(message); err != nil {
		return err
	}
	if !p.ComposerIsEmpty(output) {
		return errors.Join(ports.ErrTerminalMessageNotReady, errors.New("codebuddy requires an initialized empty composer; resolve native decisions or submit/clear your draft in the terminal"))
	}
	return nil
}

// TerminalMessageObserved confirms that AO's submission left the idle composer:
// the native turn started (spinner above the composer), the transcript above the
// composer gained one more committed "> " echo holding the whole message, or the
// transcript now ends with that echo where the idle baseline ended with a reply.
// The last case holds even when an earlier identical echo scrolled out of view.
// Only letters and digits are compared, since the echo may wrap or drop markup.
func (p *Plugin) TerminalMessageObserved(message, baseline, screen string) bool {
	if screen == baseline || !p.ComposerIsEmpty(baseline) {
		return false
	}
	if p.InspectTerminalSurface(screen).Work == ports.TerminalSurfaceWorkActive {
		return true
	}
	now, before := committedEchoes(message, screen), committedEchoes(message, baseline)
	return now.count > before.count || now.trailing && !before.trailing
}

// echoKey normalizes text for echo comparison. A message with no letters or
// digits keeps every non-space rune instead, so it can still be confirmed.
func echoKey(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	if b.Len() > 0 {
		return b.String()
	}
	for _, r := range text {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// echoes describes the committed "> " user-message echoes of one message above
// the current composer: how many there are, and whether one is the last thing
// in the transcript.
type echoes struct {
	count    int
	trailing bool
}

// committedEchoes finds "> " user-message echoes above the current composer
// whose full text, including indented continuation lines, is message.
func committedEchoes(message, screen string) echoes {
	var found echoes
	want := echoKey(message)
	if want == "" {
		return found
	}
	lines := terminalui.PlainTerminalLines(strings.TrimRight(screen, "\r\n "))
	end := transcriptEnd(lines)
	last := end - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	for i := 0; i < end; i++ {
		text := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(text, ">") {
			continue
		}
		var echo strings.Builder
		echo.WriteString(strings.TrimPrefix(text, ">"))
		for i+1 < end {
			next := lines[i+1]
			trimmed := strings.TrimSpace(next)
			if trimmed == "" || trimmed == next || strings.HasPrefix(trimmed, ">") || horizontalRule(next) {
				break
			}
			echo.WriteString(" " + trimmed)
			i++
		}
		if echoKey(echo.String()) == want {
			found.count++
			found.trailing = found.trailing || i == last
		}
	}
	return found
}

// transcriptEnd is the index of the current composer's upper rule, so the
// composer itself is never read as a committed echo.
func transcriptEnd(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
			continue
		}
		upper := i - 1
		for upper >= 0 && strings.TrimSpace(lines[upper]) == "" {
			upper--
		}
		if upper >= 0 && horizontalRule(lines[upper]) {
			return upper
		}
		return i
	}
	return len(lines)
}

// ComposerIsEmpty recognizes the current idle composer.
func (p *Plugin) ComposerIsEmpty(output string) bool {
	o := p.InspectTerminalSurface(output)
	return o.Work == ports.TerminalSurfaceWorkIdle && o.Composer == ports.TerminalComposerEmpty
}

// DetectTerminalActivity maps the current native surface to activity.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	switch p.InspectTerminalSurface(output).Work {
	case ports.TerminalSurfaceWorkActive:
		return domain.ActivityActive, true
	case ports.TerminalSurfaceWorkIdle:
		return domain.ActivityIdle, true
	case ports.TerminalSurfaceWorkWaitingInput:
		return domain.ActivityWaitingInput, true
	default:
		return "", false
	}
}

// InspectTerminalSurface interprets only the caller's current rendered viewport. A raw ring tail can
// retain overwritten spinner/composer frames and must never authorize input.
func (*Plugin) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	output = strings.TrimRight(output, "\r\n ")
	lines := terminalui.PlainTerminalLines(output)
	start := max(0, len(lines)-24)
	recent := strings.Join(lines[start:], "\n")
	unframedSurface := func() ports.TerminalSurfaceObservation {
		// Native decisions replace the framed composer. Text in a completed
		// conversation can contain the same words, so only inspect cues when
		// the current input surface is not an initialized composer.
		for _, cue := range []string{"Do you want to", "Would you like to", "Select login method:", "Enter to confirm", "Enter to login", "No, and tell CodeBuddy", "Allow this", "Do you trust the files", "Enter to select"} {
			if strings.Contains(recent, cue) {
				return ports.TerminalSurfaceObservation{Work: ports.TerminalSurfaceWorkWaitingInput}
			}
		}
		return ports.TerminalSurfaceObservation{}
	}
	composer := terminalui.LastBorderedPromptComposerState(output, ">")
	o := ports.TerminalSurfaceObservation{}
	switch composer {
	case terminalui.ComposerEmpty:
		o.Composer = ports.TerminalComposerEmpty
	case terminalui.ComposerDraft:
		o.Composer = ports.TerminalComposerDraft
	default:
		return unframedSurface()
	}
	// Require real footer chrome and an adjacent current framed composer.
	prompt := -1
	for i := len(lines) - 1; i >= start; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
			prompt = i
			break
		}
	}
	if prompt < 0 {
		return unframedSurface()
	}
	footer := strings.Join(lines[prompt+1:], "\n")
	if !strings.Contains(footer, "← for agents") && !strings.Contains(footer, "? for shortcuts") {
		return unframedSurface()
	}
	upper := prompt - 1
	for upper >= 0 && strings.TrimSpace(lines[upper]) == "" {
		upper--
	}
	if upper < 0 || !horizontalRule(lines[upper]) {
		return unframedSurface()
	}
	for i := upper - 1; i >= max(0, upper-5); i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "└ Tip:") {
			continue
		}
		first, _ := utf8.DecodeRuneInString(line)
		if strings.ContainsRune("✶✸✹✺✷", first) && strings.Contains(line, "…") && strings.Contains(line, "(") {
			o.Work = ports.TerminalSurfaceWorkActive
			return o
		}
		break
	}
	o.Work = ports.TerminalSurfaceWorkIdle
	return o
}

func horizontalRule(line string) bool {
	line = strings.TrimSpace(line)
	return utf8.RuneCountInString(line) >= 8 && strings.Trim(line, "─━") == ""
}
