package freebuff

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Freebuff's composer placeholder names the run state: "Enter a coding task"
// when idle, "Add to the current task" while a run is in flight (Enter steers
// the running task). The status bar shows "thinking..." or "working..." next to
// a "■ Esc" stop button during a run.
const (
	idlePlaceholder   = "Enter a coding task or / for commands"
	activePlaceholder = "Add to the current task (/ for commands)"
)

var activeStatus = regexp.MustCompile(`^(?:thinking|working)\.{3}(?:\s.*)?\s■ Esc(?:\s.*)?$`)
var nativeCursorSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// PromptReadinessHints retains a short settling delay after initialization.
// InitializeTerminal already requires a positively empty current composer;
// raw text patterns could mistake a historical prompt for readiness.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{
		InitialDelay: 500 * time.Millisecond,
		Timeout:      60 * time.Second,
		PollInterval: 200 * time.Millisecond,
		Lines:        60,
	}, ctx.Err()
}

// ComposerIsEmpty requires Freebuff's current input chrome and placeholder.
// Unlike a dim-styled placeholder, Freebuff's placeholder uses a normal muted
// color. Its focused empty input inserts a bold cursor before the placeholder;
// a cursor over real draft text highlights its first character instead. Plain
// captures and unfocused inputs cannot prove that distinction and fail closed.
func (*Plugin) ComposerIsEmpty(output string) bool {
	composer, ok := currentComposer(output)
	return ok && composer.placeholder != "" && composer.placeholderCursor
}

// ContinuouslyDetectTerminalActivity samples Freebuff's screen every tick: it
// has no hooks, so the terminal is the only activity signal.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// TerminalActivityOptions retains the cursor styling needed to distinguish a
// real placeholder from a human draft containing the same words.
func (*Plugin) TerminalActivityOptions() ports.TerminalActivityOptions {
	return ports.TerminalActivityOptions{PreferStyledOutput: true}
}

// DetectTerminalActivity reads Freebuff's current composer and status bar.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	composer, ok := currentComposer(output)
	if !ok {
		return "", false
	}
	if activeStatus.MatchString(composer.status) || composer.status == "high demand — in line, starting soon..." {
		return domain.ActivityActive, true
	}
	if composer.placeholder == idlePlaceholder {
		return domain.ActivityIdle, true
	}
	return "", false
}

type terminalComposer struct {
	status            string
	content           string
	placeholder       string
	placeholderCursor bool
}

// currentComposer recognizes the last complete composer immediately above
// Freebuff's native footer. Transcript words, quoted prompts, partial captures
// and changed layouts do not provide authoritative activity or draft evidence.
func currentComposer(output string) (terminalComposer, bool) {
	raw := strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n")
	lines := terminalui.PlainTerminalLines(output)
	if len(raw) != len(lines) {
		return terminalComposer{}, false
	}
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 2 || strings.TrimSpace(lines[last]) != "← for history · ? for help" {
		return terminalComposer{}, false
	}
	// The model/path/chat title can wrap. The closest complete bottom border
	// separates that footer from the input; the title's text is never input.
	for bottom := last - 1; bottom >= 0 && bottom >= last-12; bottom-- {
		width := composerBorderWidth(lines[bottom], '╰', '╯')
		if width == 0 {
			continue
		}
		if !nativeFooter(lines[bottom+1 : last]) {
			return terminalComposer{}, false
		}
		for top := bottom - 1; top >= 0; top-- {
			if composerBorderWidth(lines[top], '╭', '╮') > 0 {
				if composerBorderWidth(lines[top], '╭', '╮') != width || top+1 == bottom {
					return terminalComposer{}, false
				}
				contents := make([]string, 0, bottom-top-1)
				styled := make([]string, 0, bottom-top-1)
				for i := top + 1; i < bottom; i++ {
					row := []rune(strings.TrimSpace(lines[i]))
					if len(row) < 2 || ansi.StringWidth(string(row)) != width || row[0] != '│' || row[len(row)-1] != '│' {
						return terminalComposer{}, false
					}
					contents = append(contents, string(row[1:len(row)-1]))
					// The first visible border is outside the input; keep all
					// subsequent SGR sequences, including the cursor's bold.
					styled = append(styled, raw[i][strings.Index(raw[i], "│")+len("│"):])
				}
				return classifyComposer(lines, top, contents, styled), true
			}
			// Interior rows must be complete; do not look through another
			// box or clipped content to an older transcript border.
			if !strings.HasPrefix(strings.TrimSpace(lines[top]), "│") {
				return terminalComposer{}, false
			}
		}
		return terminalComposer{}, false
	}
	// Short terminals use a borderless input with a native ❯ prompt. Require
	// the full footer and placeholder cursor before accepting this narrower
	// layout; arbitrary transcript prompts are insufficient.
	for top := last - 2; top >= 0 && top >= last-12; top-- {
		line := strings.TrimSpace(lines[top])
		if strings.HasPrefix(line, "❯") {
			for footer := top + 1; footer < last; footer++ {
				if !nativeFooter(lines[footer:last]) {
					continue
				}
				contents := append([]string{strings.TrimPrefix(line, "❯")}, lines[top+1:footer]...)
				styled := append([]string{raw[top][strings.Index(raw[top], "❯")+len("❯"):]}, raw[top+1:footer]...)
				composer := classifyComposer(lines, top, contents, styled)
				if composer.placeholder != "" && composer.placeholderCursor {
					return composer, true
				}
			}
			return terminalComposer{}, false
		}
	}
	return terminalComposer{}, false
}

func nativeFooter(lines []string) bool {
	rows := make([]string, len(lines))
	for i, line := range lines {
		rows[i] = strings.TrimSpace(line)
		if strings.HasPrefix(rows[i], "│") {
			return false
		}
	}
	return strings.Contains(strings.Join(rows, " "), " · /model to change · Chat: ")
}

func classifyComposer(lines []string, top int, contents, styled []string) terminalComposer {
	composer := terminalComposer{}
	if top > 0 {
		composer.status = strings.TrimSpace(lines[top-1])
	}
	var text []string
	var content []string
	for i, row := range contents {
		row = strings.TrimSpace(row)
		content = append(content, withoutNativeCursor(row, styled[i]))
		if row == "" {
			continue
		}
		if len(text) == 0 {
			composer.placeholderCursor = terminalui.LastPromptHasBoldMarker(styled[i], "▍")
		}
		text = append(text, row)
	}
	value := strings.Join(text, " ")
	composer.content = strings.TrimSpace(strings.Join(content, "\n"))
	if !strings.HasPrefix(value, "▍") {
		return composer
	}
	value = strings.TrimPrefix(value, "▍")
	switch value {
	case idlePlaceholder, activePlaceholder:
		composer.placeholder = value
	case "Enter a coding task": // Freebuff abbreviates below 65 columns.
		composer.placeholder = idlePlaceholder
	case "Add to the current task":
		composer.placeholder = activePlaceholder
	}
	return composer
}

// The native cursor can sit at either end or inside an input row. Preserve
// literal human-authored ▍ characters; remove only glyphs whose current SGR
// state marks them as the native bold cursor.
func withoutNativeCursor(content, styled string) string {
	var cursor []bool
	for offset := 0; offset < len(styled); {
		position := strings.Index(styled[offset:], "▍")
		if position < 0 {
			break
		}
		position += offset
		styles := strings.Join(nativeCursorSGR.FindAllString(styled[:position], -1), "")
		cursor = append(cursor, terminalui.LastPromptHasBoldMarker(styles+"▍", "▍"))
		offset = position + len("▍")
	}
	var text strings.Builder
	index := 0
	for _, r := range content {
		if r == '▍' {
			remove := index < len(cursor) && cursor[index]
			index++
			if remove {
				continue
			}
		}
		text.WriteRune(r)
	}
	return text.String()
}

func composerBorderWidth(line string, left, right rune) int {
	runes := []rune(strings.TrimSpace(line))
	if len(runes) < 4 || runes[0] != left || runes[len(runes)-1] != right {
		return 0
	}
	for _, r := range runes[1 : len(runes)-1] {
		if r != '─' {
			return 0
		}
	}
	return len(runes)
}
