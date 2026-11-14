package chat

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const skillMetadataLimit = 64 << 10

const skillCommandPrefix = "skill:"

type skillMetadataFile struct {
	Interface struct {
		DisplayName      string `yaml:"display_name"`
		ShortDescription string `yaml:"short_description"`
	} `yaml:"interface"`
}

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

func skillFolderName(name string) (string, bool) {
	folder := strings.TrimPrefix(strings.TrimSpace(name), skillCommandPrefix)
	if folder == "" || folder == "." || folder == ".." || strings.ContainsAny(folder, `/\:`) {
		return "", false
	}
	return folder, true
}

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
