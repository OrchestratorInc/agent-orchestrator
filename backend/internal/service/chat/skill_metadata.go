package chat

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// skillMetadataLimit caps how much of one metadata file is read. The file holds a
// name and a sentence; anything larger is not one.
const skillMetadataLimit = 64 << 10

// skillCommandPrefix is how Pi-style ACP agents list a skill: `skill:<name>`.
const skillCommandPrefix = "skill:"

// skillMetadataFile is the display metadata a skill can carry in
// `agents/openai.yaml`, the file Codex reads for its own skill list.
type skillMetadataFile struct {
	Interface struct {
		DisplayName      string `yaml:"display_name"`
		ShortDescription string `yaml:"short_description"`
	} `yaml:"interface"`
}

// skillRoots lists the folders skills are installed in, the session's own
// workspace first so a project skill wins over a same-named user skill.
func skillRoots(workspace, home string) []string {
	var roots []string
	for _, base := range []string{strings.TrimSpace(workspace), home} {
		if base == "" {
			continue
		}
		roots = append(roots,
			filepath.Join(base, ".agents", "skills"),
			filepath.Join(base, ".claude", "skills"),
			filepath.Join(base, ".codex", "skills"),
		)
	}
	if home != "" {
		roots = append(roots, filepath.Join(home, ".pi", "agent", "skills"))
	}
	return roots
}

// withSkillMetadata gives skills their own display name and short description
// when the agent listed only the invocation name.
//
// ACP agents such as Claude report a skill by its folder name (`better-interface`)
// and its full trigger text, written for the model. The skill's own
// `agents/openai.yaml` says how it should read (`Better Interface`, a one-line
// summary), which is what Codex already shows. Name stays untouched: it is what
// the composer inserts and the agent runs. A skill with no metadata file keeps
// the name it was listed by; casing is never guessed.
func withSkillMetadata(skills []ports.ChatSkill, roots []string) []ports.ChatSkill {
	if len(roots) == 0 {
		return skills
	}
	out := make([]ports.ChatSkill, len(skills))
	copy(out, skills)
	for i := range out {
		skill := &out[i]
		if skill.DisplayName != "" && skill.DisplayName != skill.Name {
			continue
		}
		folder, ok := skillFolderName(skill.Name)
		if !ok {
			continue
		}
		meta, found := readSkillMetadata(roots, folder)
		if !found {
			continue
		}
		if name := strings.TrimSpace(meta.Interface.DisplayName); name != "" {
			skill.DisplayName = name
		}
		if summary := strings.TrimSpace(meta.Interface.ShortDescription); summary != "" {
			skill.Description = summary
		}
	}
	return out
}

// skillFolderName is the folder a listed skill name lives in, when the name maps
// to exactly one path segment. A namespaced plugin skill (`plugin:skill`) lives
// elsewhere and is left as listed.
func skillFolderName(name string) (string, bool) {
	folder := strings.TrimPrefix(strings.TrimSpace(name), skillCommandPrefix)
	if folder == "" || folder == "." || folder == ".." || strings.ContainsAny(folder, `/\:`) {
		return "", false
	}
	return folder, true
}

// readSkillMetadata reads the metadata of the skill folder the agent resolves:
// the first root that holds it. That folder alone is read. A project skill with
// no metadata file keeps its listed name rather than borrowing the label of a
// same-named user skill the agent does not run.
func readSkillMetadata(roots []string, folder string) (skillMetadataFile, bool) {
	for _, root := range roots {
		dir := filepath.Join(root, folder)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		return readSkillMetadataFile(filepath.Join(dir, "agents", "openai.yaml"))
	}
	return skillMetadataFile{}, false
}

func readSkillMetadataFile(path string) (skillMetadataFile, bool) {
	file, err := os.Open(path)
	if err != nil {
		return skillMetadataFile{}, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, skillMetadataLimit))
	if err != nil {
		return skillMetadataFile{}, false
	}
	var meta skillMetadataFile
	if yaml.Unmarshal(data, &meta) != nil {
		return skillMetadataFile{}, false
	}
	return meta, true
}
