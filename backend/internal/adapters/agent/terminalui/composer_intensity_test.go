package terminalui

import "testing"

func TestComposerColorPayloadDoesNotChangeIntensity(t *testing.T) {
	for _, sgr := range []string{"38;5;2", "38;2;10;20;30", "48;2;22;0;2", "58:2::0:2:22", "38:5:2"} {
		t.Run(sgr, func(t *testing.T) {
			if got := LastPromptComposerState("❯ \x1b["+sgr+"munsent input", "❯"); got != ComposerDraft {
				t.Fatalf("color payload hid real input: got=%v", got)
			}
			if got := LastPromptComposerState("❯ \x1b[2m\x1b["+sgr+"mplaceholder", "❯"); got != ComposerEmpty {
				t.Fatalf("color payload reset placeholder intensity: got=%v", got)
			}
		})
	}
}
