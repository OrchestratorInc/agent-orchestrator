package claudeacp

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestHiddenCommandsKeepWhatWorksInChat(t *testing.T) {
	// /model printed its terminal usage text and could switch the model behind
	// AO's picker; these others do real work when sent as prompt text.
	for name, hidden := range map[string]bool{
		"model": true, "effort": true, "fast": true, "auto-mode-setup": true,
		"goal": false, "init": false, "security-review": false, "context": false,
		"usage": false, "compact": false, "code-review": false,
		"autocompact": false, "rename": false, "config": false, "advisor": false,
	} {
		if got := hiddenCommands(acpsdk.AvailableCommand{Name: name}); got != hidden {
			t.Errorf("/%s hidden = %v, want %v", name, got, hidden)
		}
	}
}
