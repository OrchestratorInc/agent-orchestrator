package skillassets

import (
	_ "embed"
	"path/filepath"
)

//go:embed bug-triage/SKILL.md
var bugTriageSkill []byte

// BugTriageSkillName is the skill's directory name under <dataDir>/skills.
const BugTriageSkillName = "bug-triage"

// BugTriageDir returns the absolute directory Install writes the skill into.
func BugTriageDir(dataDir string) string {
	return filepath.Join(dataDir, "skills", BugTriageSkillName)
}

// MaterializeBugTriage writes the skill into destDir, replacing any existing
// copy. The daemon uses it for its own copy under the data dir; workspace
// installs go through hookutil.InstallBugTriageSkill, which never clears a
// folder it may not own.
func MaterializeBugTriage(destDir string) error {
	return materialize(BugTriageSkillName, destDir)
}

// BugTriageSkill returns the bug-triage skill bundled with AO.
func BugTriageSkill() []byte { return append([]byte(nil), bugTriageSkill...) }
