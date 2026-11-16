package qwenacp

import acpsdk "github.com/coder/acp-go-sdk"

// qwenHiddenBuiltins are Qwen Code built-ins kept out of the `/` menu. They
// change the model, effort, language, output style, advisor or settings behind
// AO's back; start or delete sessions; handle auth or self-update; or open a
// browser or a terminal panel. /compress stays because it is Qwen's only
// compaction.
var qwenHiddenBuiltins = map[string]struct{}{
	"model": {}, "effort": {}, "language": {}, "output-style": {}, "advisor": {},
	"config": {}, "clear": {}, "delete": {}, "auth": {}, "update": {}, "docs": {},
	"bug": {}, "skills": {},
}

// hiddenCommand hides a Qwen built-in by name only when Qwen marks the entry as
// one. A user's file command may replace a built-in of the same name, and that
// command is the user's own, so it stays.
func hiddenCommand(command acpsdk.AvailableCommand) bool {
	if source, _ := command.Meta["source"].(string); source != "builtin-command" {
		return false
	}
	_, hidden := qwenHiddenBuiltins[command.Name]
	return hidden
}
