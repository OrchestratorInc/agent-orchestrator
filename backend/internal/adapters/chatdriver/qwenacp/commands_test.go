package qwenacp

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestHiddenCommandHidesOnlyQwenBuiltins(t *testing.T) {
	builtin := map[string]any{"source": "builtin-command"}
	for _, tc := range []struct {
		name    string
		command acpsdk.AvailableCommand
		hidden  bool
	}{
		{"built-in model", acpsdk.AvailableCommand{Name: "model", Meta: builtin}, true},
		{"built-in that works in chat", acpsdk.AvailableCommand{Name: "goal", Meta: builtin}, false},
		{"compaction stays", acpsdk.AvailableCommand{Name: "compress", Meta: builtin}, false},
		// A user's file command may take a built-in's name; it is theirs to run.
		{"user command named model", acpsdk.AvailableCommand{Name: "model", Meta: map[string]any{"source": "file-command"}}, false},
		{"no provenance", acpsdk.AvailableCommand{Name: "model"}, false},
	} {
		if got := hiddenCommand(tc.command); got != tc.hidden {
			t.Errorf("%s: hidden = %v, want %v", tc.name, got, tc.hidden)
		}
	}
}
