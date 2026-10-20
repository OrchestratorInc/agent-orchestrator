package skillassets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBugTriageMatchesRepositorySkill(t *testing.T) {
	source := filepath.Join("..", "..", "..", ".agents", "skills", "bug-triage", "SKILL.md")
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != BugTriage() {
		t.Fatal("bundled bug-triage rules differ from the repository skill; refresh the bundled copy")
	}
}
