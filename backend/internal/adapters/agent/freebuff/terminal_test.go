package freebuff

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const freebuffIdleInput = "\x1b[1m▍\x1b[0mEnter a coding task or / for commands"

func freebuffTerminalScreen(status string, rows ...string) string {
	return freebuffTerminalWidthScreen(76, status, rows...)
}

func freebuffTerminalWidthScreen(width int, status string, rows ...string) string {
	var screen strings.Builder
	screen.WriteString(status + "\n╭" + strings.Repeat("─", width) + "╮\n")
	for _, row := range rows {
		padding := width - len([]rune(terminalui.PlainTerminalText(row))) - 2
		if padding < 0 {
			panic("test composer row too wide")
		}
		screen.WriteString("│ " + row + strings.Repeat(" ", padding+1) + "│\n")
	}
	screen.WriteString("╰" + strings.Repeat("─", width) + "╯\n")
	screen.WriteString("GLM 5.3 Flash • high · /workspace · /model to change · Chat: New chat\n")
	screen.WriteString("← for history · ? for help\n")
	return screen.String()
}

func TestTerminalActivityUsesCurrentFreebuffChrome(t *testing.T) {
	p := New()
	for name, tc := range map[string]struct {
		output string
		state  domain.ActivityState
		known  bool
	}{
		"idle": {freebuffTerminalScreen("1h left · 13.6K (1%)    ✕ End session", "", freebuffIdleInput, ""), domain.ActivityIdle, true},
		"transcript markers cannot make idle active": {
			"working... 12s ■ Esc\nAdd to the current task\nhigh demand — in line\n" + freebuffTerminalScreen("1h left · 13.6K (1%)    ✕ End session", freebuffIdleInput),
			domain.ActivityIdle, true,
		},
		"working":                          {freebuffTerminalScreen("working...   12s  ■ Esc", "\x1b[1m▍\x1b[0mAdd to the current task (/ for commands)"), domain.ActivityActive, true},
		"thinking":                         {freebuffTerminalScreen("thinking...   1s  ■ Esc", "\x1b[1m▍\x1b[0mAdd to the current task (/ for commands)"), domain.ActivityActive, true},
		"queue":                            {freebuffTerminalScreen("high demand — in line, starting soon...", "\x1b[1m▍\x1b[0mAdd to the current task (/ for commands)"), domain.ActivityActive, true},
		"queue with a draft":               {freebuffTerminalScreen("high demand — in line, starting soon...", "keep this draft"), domain.ActivityActive, true},
		"active with a draft":              {freebuffTerminalScreen("working...   12s  ■ Esc", "please explain Enter a coding task"), domain.ActivityActive, true},
		"queued run uses idle placeholder": {freebuffTerminalScreen("working...   12s  ■ Esc", freebuffIdleInput), domain.ActivityActive, true},
		"draft status words":               {freebuffTerminalScreen("", "thinking... working... ■ Esc"), "", false},
		"draft active placeholder":         {freebuffTerminalScreen("", "Add to the current task (/ for commands)"), "", false},
		"draft exact idle phrase":          {freebuffTerminalScreen("", "Enter a coding task or / for commands"), "", false},
		"transcript only":                  {"working...\n■ Esc\nEnter a coding task or / for commands\n", "", false},
		"old synthetic prompt":             {"> Enter a coding task or / for commands\nunlimited\n", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			state, known := p.DetectTerminalActivity(tc.output)
			if state != tc.state || known != tc.known {
				t.Fatalf("DetectTerminalActivity() = %q, %v; want %q, %v", state, known, tc.state, tc.known)
			}
		})
	}
}

func TestComposerIsEmptyRequiresCurrentFreebuffPlaceholder(t *testing.T) {
	p := New()
	for name, tc := range map[string]struct {
		output string
		empty  bool
	}{
		"native idle placeholder":            {freebuffTerminalScreen("", "", freebuffIdleInput, ""), true},
		"native active placeholder":          {freebuffTerminalScreen("working... 1s ■ Esc", "\x1b[1m▍\x1b[0mAdd to the current task (/ for commands)"), true},
		"transcript placeholder above draft": {"Enter a coding task or / for commands\n" + freebuffTerminalScreen("", "keep this draft"), false},
		"draft contains placeholder":         {freebuffTerminalScreen("", "explain Enter a coding task or / for commands"), false},
		"exact phrase is a draft":            {freebuffTerminalScreen("", "Enter a coding task or / for commands\x1b[1m▍\x1b[0m"), false},
		"unstyled placeholder is ambiguous":  {freebuffTerminalScreen("", "▍Enter a coding task or / for commands"), false},
		"unfocused phrase is ambiguous":      {freebuffTerminalScreen("", "Enter a coding task or / for commands"), false},
		"wrapped draft below placeholder":    {freebuffTerminalScreen("", freebuffIdleInput, "do not overwrite this"), false},
		"blank visible draft rows":           {freebuffTerminalScreen("", "", "\x1b[1m▍\x1b[0m"), false},
		"status words are a draft":           {freebuffTerminalScreen("", "working... ■ Esc"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := p.ComposerIsEmpty(tc.output); got != tc.empty {
				t.Fatalf("ComposerIsEmpty() = %v; want %v", got, tc.empty)
			}
		})
	}
}

func TestFreebuffTerminalPartialScreensFailClosed(t *testing.T) {
	complete := freebuffTerminalScreen("working... 12s ■ Esc", "\x1b[1m▍\x1b[0mAdd to the current task (/ for commands)")
	lines := strings.Split(complete, "\n")
	for name, output := range map[string]string{
		"missing top border":    strings.Join(append(append([]string{}, lines[:1]...), lines[2:]...), "\n"),
		"missing bottom border": strings.Join(append(append([]string{}, lines[:3]...), lines[4:]...), "\n"),
		"missing footer":        strings.Join(lines[:4], "\n"),
		"missing side border":   strings.ReplaceAll(complete, "│ ", "  "),
		"mismatched borders":    strings.ReplaceAll(complete, "╰─", "╰"),
		"truncated placeholder": freebuffTerminalScreen("", "\x1b[1m▍\x1b[0mEnter a coding task or /"),
		"compact draft":         "❯ keep this draft\nGLM 5.3 Flash · /workspace · /model to change · Chat: New chat\n← for history · ? for help\n",
	} {
		t.Run(name, func(t *testing.T) {
			if New().ComposerIsEmpty(output) {
				t.Fatal("partial screen must not prove an empty composer")
			}
			if state, known := New().DetectTerminalActivity(output); known {
				t.Fatalf("partial screen returned authoritative activity %q", state)
			}
		})
	}
}

func TestFreebuffTerminalRecognizesCompleteWrappedAndCompactChrome(t *testing.T) {
	wrappedTitle := strings.ReplaceAll(freebuffTerminalScreen("", freebuffIdleInput), "Chat: New chat", "Chat: A long\nchat title")
	wrappedFooter := strings.ReplaceAll(wrappedTitle, "/model to change", "/model\nto change")
	wrappedPlaceholder := freebuffTerminalScreen("", "\x1b[1m▍\x1b[0mEnter a coding task", "or / for commands")
	narrow := freebuffTerminalWidthScreen(27, "", "\x1b[1m▍\x1b[0mEnter a coding task")
	compact := "Freebuff\n❯ " + freebuffIdleInput + "\nGLM 5.3 Flash • high · /workspace · /model to change · Chat: New chat\n← for history · ? for help\n"
	for name, output := range map[string]string{
		"wrapped title":       wrappedTitle,
		"wrapped footer":      wrappedFooter,
		"wrapped placeholder": wrappedPlaceholder,
		"narrow":              narrow,
		"compact":             compact,
	} {
		t.Run(name, func(t *testing.T) {
			if !New().ComposerIsEmpty(output) {
				t.Fatal("complete native placeholder should prove an empty composer")
			}
			if state, known := New().DetectTerminalActivity(output); !known || state != domain.ActivityIdle {
				t.Fatalf("activity = %q, %v; want idle, true", state, known)
			}
		})
	}
}

func TestFreebuffPromptReadinessAvoidsTranscriptPatterns(t *testing.T) {
	hints, err := New().PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hints.Patterns) != 0 {
		t.Fatalf("raw readiness patterns %q can match historical transcript", hints.Patterns)
	}
}
