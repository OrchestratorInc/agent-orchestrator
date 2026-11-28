package chat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeSkillMetadata(t *testing.T, root, folder, body string) {
	t.Helper()
	dir := filepath.Join(root, folder, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWithSkillMetadataNamesListedSkillsFromTheirOwnFiles(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	roots := skillRoots(workspace, home)
	writeSkillMetadata(t, filepath.Join(home, ".agents", "skills"), "better-interface",
		"interface:\n  display_name: \"Better Interface\"\n  short_description: \"Cross-discipline interface review\"\n")
	// A project skill wins over a user skill of the same name.
	writeSkillMetadata(t, filepath.Join(home, ".claude", "skills"), "review", "interface:\n  display_name: \"User Review\"\n")
	writeSkillMetadata(t, filepath.Join(workspace, ".claude", "skills"), "review", "interface:\n  display_name: \"Project Review\"\n")

	listed := []ports.ChatSkill{
		{Name: "better-interface", DisplayName: "better-interface", Description: "Long trigger text (user)", Source: "agent"},
		{Name: "skill:better-interface", DisplayName: "skill:better-interface", Source: "agent"},
		{Name: "review", DisplayName: "review", Source: "agent"},
		{Name: "blast-radius", DisplayName: "blast-radius", Description: "Find what a change could break", Source: "agent"},
		{Name: "anthropic-skills:deep-research", DisplayName: "anthropic-skills:deep-research", Source: "agent"},
		{Name: "better-interface", DisplayName: "Codex Label", Source: "user"},
	}
	got := withSkillMetadata(listed, roots)

	want := []ports.ChatSkill{
		{Name: "better-interface", DisplayName: "Better Interface", Description: "Cross-discipline interface review", Source: "agent"},
		{Name: "skill:better-interface", DisplayName: "Better Interface", Description: "Cross-discipline interface review", Source: "agent"},
		{Name: "review", DisplayName: "Project Review", Source: "agent"},
		// No metadata file: the listed name stays exactly as it was.
		{Name: "blast-radius", DisplayName: "blast-radius", Description: "Find what a change could break", Source: "agent"},
		{Name: "anthropic-skills:deep-research", DisplayName: "anthropic-skills:deep-research", Source: "agent"},
		// A label the agent already gave is kept.
		{Name: "better-interface", DisplayName: "Codex Label", Source: "user"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d skills, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("skill %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if listed[0].DisplayName != "better-interface" {
		t.Errorf("input list was modified: %+v", listed[0])
	}
}

// The agent runs the project skill, so a same-named user skill's label must not
// stand in for it when the project skill has no metadata of its own.
func TestWithSkillMetadataKeepsAProjectSkillWithoutMetadataAsListed(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".claude", "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillMetadata(t, filepath.Join(home, ".claude", "skills"), "review",
		"interface:\n  display_name: \"User Review\"\n  short_description: \"The user's review\"\n")

	listed := []ports.ChatSkill{{Name: "review", DisplayName: "review", Description: "Project review", Source: "agent"}}
	got := withSkillMetadata(listed, skillRoots(workspace, home))
	if got[0] != listed[0] {
		t.Errorf("skill = %+v, want it unchanged %+v", got[0], listed[0])
	}
}

func TestSkillFolderNameRejectsPathsAndNamespaces(t *testing.T) {
	for _, name := range []string{"", "..", "a/b", `a\b`, "plugin:skill", "skill:"} {
		if folder, ok := skillFolderName(name); ok {
			t.Errorf("skillFolderName(%q) = %q, want rejected", name, folder)
		}
	}
	if folder, ok := skillFolderName("skill:Break"); !ok || folder != "Break" {
		t.Errorf("skillFolderName(skill:Break) = %q, %v", folder, ok)
	}
}
