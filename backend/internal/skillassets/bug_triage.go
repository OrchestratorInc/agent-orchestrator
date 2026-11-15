package skillassets

import (
	_ "embed"
	"path/filepath"
)

//go:embed bug-triage/SKILL.md
var bugTriageSkill []byte

const BugTriageSkillName = "bug-triage"

func BugTriageDir(dataDir string) string {
	return filepath.Join(dataDir, "skills", BugTriageSkillName)
}

func MaterializeBugTriage(destDir string) error {
	return materialize(BugTriageSkillName, destDir)
}

func BugTriageSkill() []byte { return append([]byte(nil), bugTriageSkill...) }
