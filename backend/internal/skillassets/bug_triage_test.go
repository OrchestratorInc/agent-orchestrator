package skillassets

import (
	"bytes"
	"os"
	"testing"
)

func TestBugTriageEmbeddedCopyMatchesCanonicalToPreventDrift(t *testing.T) {
	canonical, err := os.ReadFile("../../../.agents/skills/bug-triage/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, BugTriageSkill()) {
		t.Fatal("embedded bug-triage skill differs from canonical skill")
	}
}
