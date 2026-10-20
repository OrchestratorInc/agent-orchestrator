package skillassets

import _ "embed"

//go:embed bug-triage/SKILL.md
var bugTriagePlaybook []byte

// BugTriagePlaybook returns the canonical triage instructions bundled with AO.
func BugTriagePlaybook() []byte { return append([]byte(nil), bugTriagePlaybook...) }
