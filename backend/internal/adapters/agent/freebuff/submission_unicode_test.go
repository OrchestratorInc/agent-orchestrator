package freebuff

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Real terminal rows are padded to display cells, not Unicode rune counts.
func cellPaddedComposer(t *testing.T, rows ...string) string {
	t.Helper()
	const width = 100
	var screen strings.Builder
	screen.WriteString("unlimited\n╭" + strings.Repeat("─", width) + "╮\n")
	for _, row := range rows {
		padding := width - ansi.StringWidth(terminalui.PlainTerminalText(row)) - 2
		if padding < 0 {
			t.Fatal("fixture input must be wrapped into the terminal width")
		}
		screen.WriteString("│ " + row + strings.Repeat(" ", padding+1) + "│\n")
	}
	screen.WriteString("╰" + strings.Repeat("─", width) + "╯\n" + startupPickerFooter)
	return screen.String()
}

func TestFirstTurnUnicodeDraftUsesTerminalCellWidths(t *testing.T) {
	for name, rows := range map[string][]string{
		"CJK":              {"Remember the harmless marker 蓝宝石_6553 and wait."},
		"emoji":            {"Remember the harmless marker 🚀_6553 and wait."},
		"combining accent": {"Remember the harmless marker cafe\u0301_6553 and wait."},
		"joined emoji":     {"Remember the harmless marker 👩‍💻_6553 and wait."},
		"wrapped mixed input": {
			"This harmless multiline prompt contains 中文 and a rocket 🚀.",
			"Preserve the combining accent in cafe\u0301 and the joined emoji 👩‍💻.",
			"Reply exactly UNICODE_6553_OK, then wait.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, cfg := submissionFixture(t)
			prompt := strings.Join(rows, "\n")
			visible := append([]string(nil), rows...)
			visible[len(visible)-1] += "\x1b[1m▍\x1b[0m"
			output := cellPaddedComposer(t, visible...)
			state, err := p.AfterStartSubmitState(context.Background(), cfg, output, prompt)
			if err != nil || state != ports.AfterStartDraftPending {
				t.Fatalf("valid Unicode draft state=%q err=%v", state, err)
			}
			if p.ComposerIsEmpty(output) {
				t.Fatal("nonempty Unicode input was considered empty")
			}
		})
	}
}
