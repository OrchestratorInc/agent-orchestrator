package chat

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// skillFileLimit caps how much of a SKILL.md or command file is read. Only the
// frontmatter at its top matters.
const skillFileLimit = 64 << 10

// skillFrontmatter is the part of a SKILL.md (or a command's .md) a menu needs.
type skillFrontmatter struct {
	Name          string `yaml:"name"`
	Description   string `yaml:"description"`
	ArgumentHint  string `yaml:"argument-hint"`
	UserInvocable *bool  `yaml:"user-invocable"`
}

var frontmatterPattern = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---(?:\r?\n|\z)`)

// skillRoot is one folder of skills or commands, in the order the agent resolves them.
type skillRoot struct {
	dir string
	// prefix namespaces a plugin's entries the way the agent lists them: `plugin:name`.
	prefix string
	// commands marks a folder of `<name>.md` commands rather than `<name>/SKILL.md` skills.
	commands bool
	// source is what the agent itself reports, so the menu's badge does not change
	// when a session takes over the list.
	source string
	// byFrontmatter names a skill by its SKILL.md `name`, as Codex does; Claude
	// names it by its folder.
	byFrontmatter bool
}

// ScanSkills lists the skills an agent would offer in a workspace, read from the
// folders the agent itself loads them from, so a chat can show its `/` menu before
// the agent has started. A running session still asks the agent (Skills): the agent
// also lists its built-in commands, which no folder holds.
//
// Only agents whose skill folders AO knows are scanned; any other agent gets nil,
// and the client keeps whatever list a live session of that agent last reported.
func ScanSkills(agentID, workspace, home string) []ports.ChatSkill {
	workspace = strings.TrimSpace(workspace)
	var roots []skillRoot
	switch domain.AgentHarness(agentID) {
	case domain.HarnessClaudeCode:
		roots = claudeSkillRoots(workspace, home)
	case domain.HarnessCodex:
		roots = codexSkillRoots(workspace, home)
	default:
		return nil
	}
	return scanSkillRoots(roots)
}

func claudeConfigDir(home string) string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeSkillRoots mirrors Claude Code's own order: the project's skills and
// commands, then the user's, then each enabled plugin's under its own name.
func claudeSkillRoots(workspace, home string) []skillRoot {
	config := claudeConfigDir(home)
	var roots []skillRoot
	// A live Claude session reports every entry as "agent" (ACP carries no
	// provenance), so these do too: the menu's badge stays put when the chat starts.
	const source = "agent"
	if workspace != "" {
		roots = append(roots,
			skillRoot{dir: filepath.Join(workspace, ".claude", "skills"), source: source},
			skillRoot{dir: filepath.Join(workspace, ".claude", "commands"), commands: true, source: source},
		)
	}
	if config != "" {
		roots = append(roots,
			skillRoot{dir: filepath.Join(config, "skills"), source: source},
			skillRoot{dir: filepath.Join(config, "commands"), commands: true, source: source},
		)
		for _, plugin := range enabledClaudePlugins(config, workspace) {
			roots = append(roots,
				skillRoot{dir: filepath.Join(plugin.path, "skills"), prefix: plugin.name, source: source},
				skillRoot{dir: filepath.Join(plugin.path, "commands"), prefix: plugin.name, commands: true, source: source},
			)
		}
	}
	return roots
}

// codexSkillRoots are the folders Codex scans: the repo's, then the user's,
// then the skills Codex ships with.
func codexSkillRoots(workspace, home string) []skillRoot {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" && home != "" {
		codexHome = filepath.Join(home, ".codex")
	}
	var roots []skillRoot
	if workspace != "" {
		roots = append(roots,
			skillRoot{dir: filepath.Join(workspace, ".agents", "skills"), source: "repo", byFrontmatter: true},
			skillRoot{dir: filepath.Join(workspace, ".codex", "skills"), source: "repo", byFrontmatter: true},
		)
	}
	if home != "" {
		roots = append(roots, skillRoot{dir: filepath.Join(home, ".agents", "skills"), source: "user", byFrontmatter: true})
	}
	if codexHome != "" {
		roots = append(roots,
			skillRoot{dir: filepath.Join(codexHome, "skills"), source: "user", byFrontmatter: true},
			skillRoot{dir: filepath.Join(codexHome, "skills", ".system"), source: "system", byFrontmatter: true},
		)
	}
	return roots
}

type claudePlugin struct {
	name string
	path string
}

// enabledClaudePlugins lists installed plugins that the user's or project's
// settings switch on. A plugin installed for one project only counts in that project.
func enabledClaudePlugins(config, workspace string) []claudePlugin {
	enabled := map[string]bool{}
	settings := []string{filepath.Join(config, "settings.json")}
	if workspace != "" {
		settings = append(settings,
			filepath.Join(workspace, ".claude", "settings.json"),
			filepath.Join(workspace, ".claude", "settings.local.json"),
		)
	}
	for _, path := range settings {
		var file struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if readJSONFile(path, &file) {
			for key, on := range file.EnabledPlugins {
				enabled[key] = on
			}
		}
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	if !readJSONFile(filepath.Join(config, "plugins", "installed_plugins.json"), &installed) {
		return nil
	}
	keys := make([]string, 0, len(installed.Plugins))
	for key := range installed.Plugins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var plugins []claudePlugin
	seen := map[string]bool{}
	for _, key := range keys {
		if !enabled[key] {
			continue
		}
		name, _, _ := strings.Cut(key, "@")
		if name == "" {
			continue
		}
		for _, install := range installed.Plugins[key] {
			if install.InstallPath == "" {
				continue
			}
			// A plugin installed for one project loads only there; a chat with no
			// project gets none of them.
			if install.Scope != "" && install.Scope != "user" &&
				(workspace == "" || install.ProjectPath == "" || filepath.Clean(install.ProjectPath) != filepath.Clean(workspace)) {
				continue
			}
			// Claude namespaces a plugin's skills by its manifest name, which is
			// almost always the install key's.
			var manifest struct {
				Name string `json:"name"`
			}
			if readJSONFile(filepath.Join(install.InstallPath, ".claude-plugin", "plugin.json"), &manifest) && strings.TrimSpace(manifest.Name) != "" {
				name = strings.TrimSpace(manifest.Name)
			}
			// Deduplicated by the resolved name: two install keys can name the
			// same plugin, and one key's manifest can name another's.
			if !seen[name] {
				seen[name] = true
				plugins = append(plugins, claudePlugin{name: name, path: install.InstallPath})
			}
			break
		}
	}
	return plugins
}

// scanSkillRoots reads every root in order; the first entry of a name wins, as
// it does in the agent.
func scanSkillRoots(roots []skillRoot) []ports.ChatSkill {
	var skills []ports.ChatSkill
	seen := map[string]bool{}
	for _, root := range roots {
		if root.commands {
			for _, command := range commandFiles(root.dir) {
				meta, ok := readSkillFrontmatter(command.file, false)
				if !ok || (meta.UserInvocable != nil && !*meta.UserInvocable) {
					continue
				}
				name := command.name
				if root.prefix != "" {
					name = root.prefix + ":" + name
				}
				if seen[name] {
					continue
				}
				seen[name] = true
				skills = append(skills, ports.ChatSkill{
					Name:        name,
					DisplayName: name,
					Description: strings.TrimSpace(meta.Description),
					InputHint:   strings.TrimSpace(meta.ArgumentHint),
					Source:      root.source,
				})
			}
			continue
		}
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			folder := entry.Name()
			dir := filepath.Join(root.dir, folder)
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				continue
			}
			meta, ok := readSkillFrontmatter(filepath.Join(dir, "SKILL.md"), true)
			if !ok || (meta.UserInvocable != nil && !*meta.UserInvocable) {
				continue
			}
			name := folder
			if frontmatterName := strings.TrimSpace(meta.Name); root.byFrontmatter && frontmatterName != "" {
				name = frontmatterName
			}
			if root.prefix != "" {
				name = root.prefix + ":" + name
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			skill := ports.ChatSkill{
				Name:        name,
				DisplayName: name,
				Description: strings.TrimSpace(meta.Description),
				InputHint:   strings.TrimSpace(meta.ArgumentHint),
				Source:      root.source,
			}
			// The label comes from the folder that supplied the skill, never from a
			// same-named skill in another root; with none, the scanned values stay.
			if display, found := readSkillMetadataFile(filepath.Join(dir, "agents", "openai.yaml")); found {
				if label := strings.TrimSpace(display.Interface.DisplayName); label != "" {
					skill.DisplayName = label
				}
				if summary := strings.TrimSpace(display.Interface.ShortDescription); summary != "" {
					skill.Description = summary
				}
			}
			skills = append(skills, skill)
		}
	}
	return skills
}

type commandFile struct {
	name string
	file string
}

// commandFiles lists a commands folder's `.md` files, subfolders included. Claude
// names a nested command by its path: `frontend/component.md` is `frontend:component`.
func commandFiles(dir string) []commandFile {
	var files []commandFile
	// WalkDir does not follow a symlinked root, so resolve it first.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	prefix := dir + string(filepath.Separator)
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		// WalkDir reports errors only for folders (the root included): skip one it
		// cannot read and keep the rest.
		if err != nil {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			if path != dir && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		rel := strings.TrimPrefix(path, prefix)
		name := strings.ReplaceAll(filepath.ToSlash(strings.TrimSuffix(rel, ".md")), "/", ":")
		files = append(files, commandFile{name: name, file: path})
		return nil
	})
	return files
}

// readSkillFrontmatter reads a skill's frontmatter. A skill must have a file;
// a command may carry no frontmatter at all and still runs.
func readSkillFrontmatter(path string, required bool) (skillFrontmatter, bool) {
	file, err := os.Open(path)
	if err != nil {
		return skillFrontmatter{}, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, skillFileLimit))
	if err != nil {
		return skillFrontmatter{}, false
	}
	match := frontmatterPattern.FindSubmatch(data)
	if match == nil {
		return skillFrontmatter{}, !required || len(data) > 0
	}
	var meta skillFrontmatter
	if yaml.Unmarshal(match[1], &meta) != nil {
		// Malformed YAML: the agent would still list the skill by its folder.
		return skillFrontmatter{}, true
	}
	return meta, true
}

func readJSONFile(path string, out any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, out) == nil
}
