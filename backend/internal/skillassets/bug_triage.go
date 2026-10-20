package skillassets

import _ "embed"

//go:embed bug-triage/SKILL.md
var bugTriage string

// BugTriage supplies the same reporting rules to the independent desktop agent
// without relying on the user's checkout or a running daemon.
func BugTriage() string { return bugTriage }
