package chat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillNames(skills []ports.ChatSkill) []string {
	names := make([]string, len(skills))
	for i, skill := range skills {
		names[i] = skill.Name
	}
	return names
}

func TestScanSkillsListsClaudeSkillsWithoutASession(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config := filepath.Join(home, ".claude")

	writeFile(t, filepath.Join(workspace, ".claude", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: Project review\n---\nbody")
	writeFile(t, filepath.Join(config, "skills", "review", "SKILL.md"), "---\ndescription: User review\n---\n")
	writeFile(t, filepath.Join(config, "skills", "ship", "SKILL.md"), "---\ndescription: Ship it\nargument-hint: <branch>\n---\n")
	writeFile(t, filepath.Join(config, "skills", "hidden", "SKILL.md"), "---\nuser-invocable: false\n---\n")
	writeFile(t, filepath.Join(config, "skills", "empty-folder", "README.md"), "no skill here")
	writeFile(t, filepath.Join(config, "commands", "standup.md"), "Summarize yesterday.")

	on := filepath.Join(config, "plugins", "cache", "m", "figma", "1.0.0")
	off := filepath.Join(config, "plugins", "cache", "m", "warp", "1.0.0")
	writeFile(t, filepath.Join(on, "skills", "figma-use", "SKILL.md"), "---\ndescription: Use Figma\n---\n")
	writeFile(t, filepath.Join(off, "skills", "warp-thing", "SKILL.md"), "---\ndescription: Off\n---\n")
	writeFile(t, filepath.Join(config, "plugins", "installed_plugins.json"), `{"version":2,"plugins":{
		"figma@m":[{"scope":"user","installPath":"`+on+`"}],
		"warp@m":[{"scope":"user","installPath":"`+off+`"}]}}`)
	writeFile(t, filepath.Join(config, "settings.json"), `{"enabledPlugins":{"figma@m":true,"warp@m":false}}`)

	got := ScanSkills("claude-code", workspace, home)
	want := []string{"review", "ship", "standup", "figma:figma-use"}
	if names := skillNames(got); len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	} else {
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("names = %v, want %v", names, want)
			}
		}
	}
	if got[0].Description != "Project review" || got[0].Source != "agent" {
		t.Errorf("project skill should win over the user's: %+v", got[0])
	}
	if got[1].InputHint != "<branch>" {
		t.Errorf("argument hint = %q", got[1].InputHint)
	}
	if got[3].Source != "agent" {
		t.Errorf("plugin source = %q, want the live session's", got[3].Source)
	}
}

func TestScanSkillsNamesPluginSkillsByTheirManifest(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config := filepath.Join(home, ".claude")
	install := filepath.Join(config, "plugins", "cache", "m", "ce", "1.0.0")
	writeFile(t, filepath.Join(install, ".claude-plugin", "plugin.json"), `{"name":"compound-engineering"}`)
	writeFile(t, filepath.Join(install, "skills", "ce-plan", "SKILL.md"), "---\ndescription: Plan\n---\n")
	writeFile(t, filepath.Join(config, "plugins", "installed_plugins.json"), `{"plugins":{"ce@m":[{"scope":"user","installPath":"`+install+`"}]}}`)
	writeFile(t, filepath.Join(config, "settings.json"), `{"enabledPlugins":{"ce@m":true}}`)

	if names := skillNames(ScanSkills("claude-code", workspace, home)); len(names) != 1 || names[0] != "compound-engineering:ce-plan" {
		t.Fatalf("names = %v", names)
	}
}

func TestScanSkillsListsCodexSkills(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", "")
	writeFile(t, filepath.Join(workspace, ".agents", "skills", "lint", "SKILL.md"), "---\ndescription: Lint\n---\n")
	writeFile(t, filepath.Join(home, ".codex", "skills", ".system", "imagegen", "SKILL.md"), "---\ndescription: Images\n---\n")
	writeSkillMetadata(t, filepath.Join(workspace, ".agents", "skills"), "lint",
		"interface:\n  display_name: \"Lint\"\n  short_description: \"Run the linters\"\n")

	got := ScanSkills("codex", workspace, home)
	if names := skillNames(got); len(names) != 2 || names[0] != "lint" || names[1] != "imagegen" {
		t.Fatalf("names = %v", names)
	}
	if got[0].DisplayName != "Lint" || got[0].Description != "Run the linters" {
		t.Errorf("metadata not applied: %+v", got[0])
	}
}

func TestScanSkillsIgnoresAgentsWithUnknownFolders(t *testing.T) {
	if got := ScanSkills("some-agent", t.TempDir(), t.TempDir()); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestScanSkillsLeavesProjectPluginsOutOfOtherChats(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config := filepath.Join(home, ".claude")
	install := filepath.Join(config, "plugins", "cache", "m", "local", "1.0.0")
	writeFile(t, filepath.Join(install, "skills", "only-here", "SKILL.md"), "---\ndescription: Local\n---\n")
	writeFile(t, filepath.Join(config, "plugins", "installed_plugins.json"),
		`{"plugins":{"local@m":[{"scope":"project","installPath":"`+install+`","projectPath":"`+project+`"}],
		"blank@m":[{"scope":"local","installPath":"`+install+`"}]}}`)
	writeFile(t, filepath.Join(config, "settings.json"), `{"enabledPlugins":{"local@m":true,"blank@m":true}}`)

	if names := skillNames(ScanSkills("claude-code", project, home)); len(names) != 1 || names[0] != "local:only-here" {
		t.Fatalf("in its project: %v", names)
	}
	if names := skillNames(ScanSkills("claude-code", "", home)); len(names) != 0 {
		t.Fatalf("with no project: %v", names)
	}
}

func TestScanSkillsLabelsASkillFromItsOwnFolder(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeFile(t, filepath.Join(workspace, ".claude", "skills", "review", "SKILL.md"), "---\ndescription: Claude review\n---\n")
	// A same-named skill Claude does not load must not lend it a label.
	writeSkillMetadata(t, filepath.Join(workspace, ".agents", "skills"), "review",
		"interface:\n  display_name: \"Other Review\"\n  short_description: \"Not this one\"\n")
	writeFile(t, filepath.Join(workspace, ".claude", "skills", "ship", "SKILL.md"), "---\ndescription: Ship\n---\n")
	writeSkillMetadata(t, filepath.Join(workspace, ".claude", "skills"), "ship",
		"interface:\n  display_name: \"Ship It\"\n  short_description: \"Open the PR\"\n")

	got := ScanSkills("claude-code", workspace, home)
	if len(got) != 2 {
		t.Fatalf("skills = %+v", got)
	}
	if got[0].Name != "review" || got[0].DisplayName != "review" || got[0].Description != "Claude review" {
		t.Errorf("review borrowed another folder's metadata: %+v", got[0])
	}
	if got[1].DisplayName != "Ship It" || got[1].Description != "Open the PR" {
		t.Errorf("ship lost its own metadata: %+v", got[1])
	}
}

func TestScanSkillsNamesNestedCommandsAndSkillFolders(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	// Verified against Claude Code 2.1: a skill is named by its folder, and a
	// nested command by its path.
	writeFile(t, filepath.Join(workspace, ".claude", "skills", "folder-a", "SKILL.md"), "---\nname: other-b\n---\n")
	writeFile(t, filepath.Join(workspace, ".claude", "commands", "ns", "deep-cmd.md"), "Nested.")

	names := skillNames(ScanSkills("claude-code", workspace, home))
	if len(names) != 2 || names[0] != "folder-a" || names[1] != "ns:deep-cmd" {
		t.Fatalf("names = %v", names)
	}
}

func TestScanSkillsDeduplicatesPluginsByTheirResolvedName(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config := filepath.Join(home, ".claude")
	a := filepath.Join(config, "plugins", "cache", "m", "a")
	b := filepath.Join(config, "plugins", "cache", "m", "b")
	// a@m's manifest says "b"; b@m's says "c". Both must contribute.
	writeFile(t, filepath.Join(a, ".claude-plugin", "plugin.json"), `{"name":"b"}`)
	writeFile(t, filepath.Join(a, "skills", "one", "SKILL.md"), "---\ndescription: One\n---\n")
	writeFile(t, filepath.Join(b, ".claude-plugin", "plugin.json"), `{"name":"c"}`)
	writeFile(t, filepath.Join(b, "skills", "two", "SKILL.md"), "---\ndescription: Two\n---\n")
	writeFile(t, filepath.Join(config, "plugins", "installed_plugins.json"), `{"plugins":{
		"a@m":[{"scope":"user","installPath":"`+a+`"}],
		"b@m":[{"scope":"user","installPath":"`+b+`"}]}}`)
	writeFile(t, filepath.Join(config, "settings.json"), `{"enabledPlugins":{"a@m":true,"b@m":true}}`)

	names := skillNames(ScanSkills("claude-code", workspace, home))
	if len(names) != 2 || names[0] != "b:one" || names[1] != "c:two" {
		t.Fatalf("names = %v", names)
	}
}
