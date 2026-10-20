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
	if !bytes.Equal(canonical, BugTriagePlaybook()) {
		t.Fatal("embedded bug-triage playbook differs from canonical skill")
	}
}
