package skillassets

import _ "embed"

//go:embed bug-triage/SKILL.md
var bugTriageSkill []byte

// BugTriageSkill returns the bug-triage skill bundled with AO.
func BugTriageSkill() []byte { return append([]byte(nil), bugTriageSkill...) }
