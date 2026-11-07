package terminalui

import (
	"strings"
	"testing"
)

// Claude Code 2.1.314 draws a placeholder or prompt suggestion as
// reverse(first letter) + dim(rest) while it believes its terminal has focus,
// and all dim otherwise; reverse writes ESC[7m ... ESC[27m.
const (
	reverseOn  = "\x1b[7m"
	reverseOff = "\x1b[27m"
	dimOn      = "\x1b[2m"
	dimOff     = "\x1b[22m"
)

func cursorPlaceholderRow(first, rest string) string {
	return "❯ " + reverseOn + first + reverseOff + dimOn + rest + dimOff
}

func TestBorderedPromptWithCursorPlaceholderReadsSuggestionAsEmpty(t *testing.T) {
	rule := strings.Repeat("─", 48)
	frame := func(rows ...string) string {
		return rule + "\n" + strings.Join(rows, "\n") + "\n" + rule + "\nstatus line\n-- INSERT --"
	}
	for _, tt := range []struct {
		name, output string
		want         ComposerState
	}{
		{"suggestion under the cursor", frame(cursorPlaceholderRow("g", "o TD 2s fix")), ComposerEmpty},
		{"suggestion without the cursor", frame("❯ " + dimOn + "go TD 2s fix" + dimOff), ComposerEmpty},
		{"empty composer under the cursor", frame("❯ " + reverseOn + " " + reverseOff), ComposerEmpty},
		{"reset ends the cursor", frame("❯ " + reverseOn + "g\x1b[0m" + dimOn + "o TD 2s fix" + dimOff), ComposerEmpty},
		// The conpty host's emulator re-encodes the same cells with combined attributes.
		{"emulator re-encoded suggestion", frame("❯ \x1b[7mg\x1b[27;2mo TD 2s fix\x1b[m"), ComposerEmpty},
		// An empty SGR field is a reset (ECMA-48 default 0), so ESC[;2m ends the cursor.
		{"empty field resets before dim", frame("❯ \x1b[7mg\x1b[;2mo TD 2s fix\x1b[22m"), ComposerEmpty},
		{"wrapped suggestion", frame(cursorPlaceholderRow("g", "o TD 2s fix and"), "  "+dimOn+"more placeholder"+dimOff), ComposerEmpty},
		{"draft under the cursor", frame("❯ " + reverseOn + "g" + reverseOff + "o TD 2s fix"), ComposerDraft},
		{"one-letter draft under the cursor", frame("❯ " + reverseOn + "g" + reverseOff), ComposerDraft},
		{"typed text after the dim tail", frame(cursorPlaceholderRow("g", "o TD") + " typed"), ComposerDraft},
		{"typed row below the suggestion", frame(cursorPlaceholderRow("g", "o TD 2s fix"), "  typed"), ComposerDraft},
		{"cursor not on the first letter", frame("❯ g" + reverseOn + "o" + reverseOff + dimOn + " TD 2s fix" + dimOff), ComposerDraft},
		{"two reversed letters", frame("❯ " + reverseOn + "go" + reverseOff + dimOn + " TD 2s fix" + dimOff), ComposerDraft},
		{"reversed letter in the tail", frame(cursorPlaceholderRow("g", "o TD") + dimOn + reverseOn + "x" + reverseOff + dimOff), ComposerDraft},
		{"dim reset keeps reverse on", frame("❯ " + reverseOn + "g\x1b[22m" + dimOn + "o TD 2s fix" + dimOff), ComposerDraft},
		{"palette color 7 is not reverse", frame("❯ \x1b[38;5;7mg\x1b[39m" + dimOn + "o TD 2s fix" + dimOff), ComposerDraft},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := LastBorderedPromptComposerStateWithCursorPlaceholder(tt.output, "❯"); got != tt.want {
				t.Fatalf("LastBorderedPromptComposerStateWithCursorPlaceholder() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Provider chrome labels below a suggestion stay exempt, as for any composer.
func TestCursorPlaceholderSkipsChromeLabelRows(t *testing.T) {
	rule := strings.Repeat("─", 48)
	output := rule + "\n" + cursorPlaceholderRow("g", "o TD 2s fix") + "\n  provider label\n" + rule + "\nstatus"
	if got := LastBorderedPromptComposerStateWithCursorPlaceholder(output, "❯", "provider label"); got != ComposerEmpty {
		t.Fatalf("with the label named: %v, want empty", got)
	}
	if got := LastBorderedPromptComposerStateWithCursorPlaceholder(output, "❯"); got != ComposerDraft {
		t.Fatalf("without the label named: %v, want draft", got)
	}
}

// Only the reader Claude Code uses accepts the cursor. The plain bordered
// reader (also used by Hermes) and the footer-free reader keep reading any
// letter that is not dim as a draft.
func TestCursorPlaceholderIsLimitedToItsOwnReader(t *testing.T) {
	rule := strings.Repeat("─", 48)
	row := cursorPlaceholderRow("g", "o TD 2s fix")
	bordered := rule + "\n" + row + "\n" + rule + "\nstatus"
	for name, got := range map[string]ComposerState{
		"LastBorderedPromptComposerState": LastBorderedPromptComposerState(bordered, "❯"),
		"LastPromptComposerState":         LastPromptComposerState(row, "❯"),
	} {
		if got != ComposerDraft {
			t.Errorf("%s() = %v, want draft", name, got)
		}
	}
}

// An empty top-level SGR field resets every tracked attribute, as ESC[0m does.
// An empty field inside an extended color payload is color data, not a reset.
func TestSGREmptyFieldResets(t *testing.T) {
	type style struct{ dim, bold, reverse bool }
	for _, tt := range []struct {
		name, output string
		want         []style
	}{
		{"reverse then reset and dim", "\x1b[7mg\x1b[;2mo", []style{{reverse: true}, {dim: true}}},
		{"dim and bold then reset and reverse", "\x1b[2;1ma\x1b[;7mb", []style{{dim: true, bold: true}, {reverse: true}}},
		{"trailing empty field", "\x1b[2ma\x1b[1;mb", []style{{dim: true}, {}}},
		{"empty palette index keeps dim", "\x1b[2ma\x1b[38;5;mb", []style{{dim: true}, {dim: true}}},
		{"empty palette index before bold", "\x1b[2ma\x1b[38;5;;1mb", []style{{dim: true}, {dim: true, bold: true}}},
		{"empty field in a colon color", "\x1b[2ma\x1b[38:2::255:0:0mb", []style{{dim: true}, {dim: true}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lines := styledTerminalLines(tt.output)
			if len(lines) != 1 || len(lines[0]) != len(tt.want) {
				t.Fatalf("styled lines = %+v, want one line of %d cells", lines, len(tt.want))
			}
			for i, r := range lines[0] {
				if got := (style{r.dim, r.bold, r.reverse}); got != tt.want[i] {
					t.Fatalf("cell %d %q = %+v, want %+v", i, r.value, got, tt.want[i])
				}
			}
		})
	}
}
