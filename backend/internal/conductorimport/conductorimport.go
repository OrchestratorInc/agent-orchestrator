// Package conductorimport reads a repository's Conductor settings and merges
// them into an Orchestrator project config without writing anything.
package conductorimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/gitdefault"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	settingsFile      = ".conductor/settings.toml"
	localSettingsFile = ".conductor/settings.local.toml"
	legacyFile        = "conductor.json"
	includeFile       = ".worktreeinclude"
	maxFileBytes      = 1 << 20
	maxCopiedFiles    = 200
	maxCueName        = 64
	maxCueCommand     = 4096
)

var conductorVar = regexp.MustCompile(`CONDUCTOR_[A-Z0-9_]+`)

var envNames = []string{"CONDUCTOR_ROOT_PATH", "CONDUCTOR_WORKSPACE_PATH", "CONDUCTOR_WORKSPACE_NAME", "CONDUCTOR_IS_LOCAL", "CONDUCTOR_PORT", "CONDUCTOR_DEFAULT_BRANCH"}

// Env sets Conductor's workspace variables for Setup, Cleanup, terminals and
// Cues, so imported commands run unchanged.
func Env(ctx context.Context, rootPath, workspacePath string) map[string]string {
	env := map[string]string{
		"CONDUCTOR_ROOT_PATH":      rootPath,
		"CONDUCTOR_WORKSPACE_PATH": workspacePath,
		"CONDUCTOR_WORKSPACE_NAME": filepath.Base(workspacePath),
		"CONDUCTOR_IS_LOCAL":       "1",
		"CONDUCTOR_PORT":           strconv.Itoa(port(workspacePath)),
	}
	if resolution, err := gitdefault.New("git", nil).Inspect(ctx, rootPath); err == nil {
		env["CONDUCTOR_DEFAULT_BRANCH"] = resolution.Branch
	}
	return env
}

// port is the first of the ten ports Conductor gives each workspace, below
// the operating systems' ephemeral ranges.
// ponytail: hashed from the workspace path, so two workspaces share a block
// about once in 2,270 pairs; store a block per session if that matters.
func port(workspacePath string) int {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(workspacePath))
	return 10000 + int(sum.Sum32()%2270)*10
}

func setByAO(name string) bool {
	return slices.Contains(envNames, name)
}

// Item is one review row: the Conductor key and its command, names or reason.
type Item struct {
	Key    string `json:"key"`
	Detail string `json:"detail,omitempty"`
}

// CueDraft is a Conductor run action to create as a command Cue.
type CueDraft struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Plan is the project config with the Conductor settings merged in, and what the import adds, flags and leaves out.
type Plan struct {
	Source   string               `json:"source"`
	Config   domain.ProjectConfig `json:"config"`
	Cues     []CueDraft           `json:"cues"`
	Added    []Item               `json:"added"`
	Warnings []Item               `json:"warnings"`
	Skipped  []Item               `json:"skipped"`
}

// Read merges the Conductor settings in root into current. Source is empty when root has none.
func Read(ctx context.Context, root string, current domain.ProjectConfig) (Plan, error) {
	plan := Plan{Config: current, Cues: []CueDraft{}, Added: []Item{}, Warnings: []Item{}, Skipped: []Item{}}
	if root == "" || runtime.GOOS == "windows" {
		return plan, nil
	}
	repo, err := os.OpenRoot(root)
	if err != nil {
		return plan, err
	}
	defer func() { _ = repo.Close() }()
	settings, err := load(repo, &plan)
	if err != nil || plan.Source == "" {
		return plan, err
	}
	if err := plan.copyFiles(ctx, repo, root, settings); err != nil {
		return plan, err
	}
	plan.merge(settings)
	sortItems(plan.Skipped)
	return plan, nil
}

func load(repo *os.Root, plan *Plan) (map[string]any, error) {
	settings := map[string]any{}
	for _, name := range []string{settingsFile, legacyFile, localSettingsFile} {
		if name == legacyFile && plan.Source != "" {
			continue
		}
		body, err := readFile(repo, name)
		if err != nil {
			return nil, err
		}
		if body == nil {
			continue
		}
		var layer map[string]any
		if name == legacyFile {
			err = json.Unmarshal(body, &layer)
		} else {
			err = toml.Unmarshal(body, &layer)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		overlay(settings, layer)
		if plan.Source == "" {
			plan.Source = name
		}
	}
	if plan.Source == "" {
		return settings, nil
	}
	user, err := userSettings()
	if err != nil || user == nil {
		return settings, err
	}
	overlay(user, settings)
	return user, nil
}

func userSettings() (map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	dir, err := os.OpenRoot(filepath.Join(home, ".conductor"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	body, err := readFile(dir, "settings.toml")
	if err != nil || body == nil {
		return nil, err
	}
	var layer map[string]any
	if err := toml.Unmarshal(body, &layer); err != nil {
		return nil, fmt.Errorf("~/.conductor/settings.toml: %w", err)
	}
	delete(layer, "models")
	delete(layer, "tool_approvals_enabled")
	return layer, nil
}

func readFile(repo *os.Root, name string) ([]byte, error) {
	f, err := repo.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFileBytes {
		return nil, fmt.Errorf("%s is larger than 1 MiB", name)
	}
	return body, nil
}

func overlay(dst, src map[string]any) {
	for key, value := range src {
		if sub, ok := value.(map[string]any); ok {
			if into, ok := dst[key].(map[string]any); ok {
				overlay(into, sub)
				continue
			}
		}
		dst[key] = value
	}
}

func table(m map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		next, ok := m[key].(map[string]any)
		if !ok {
			return nil
		}
		m = next
	}
	return m
}

func text(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return strings.TrimSpace(value)
}

func (p *Plan) merge(settings map[string]any) {
	scripts := table(settings, "scripts")
	p.addStep(&p.Config.PostCreate, "scripts.setup", text(scripts, "setup"))
	p.addStep(&p.Config.PreRemove, "scripts.archive", text(scripts, "archive"))
	p.addCues(scripts)
	p.addEnv(table(settings, "environment_variables"))
	if rules := text(table(settings, "prompts"), "general"); rules != "" && !strings.Contains(p.Config.AgentRules, rules) {
		p.Config.AgentRules = strings.TrimSpace(p.Config.AgentRules + "\n\n" + rules)
		p.Added = append(p.Added, Item{Key: "prompts.general", Detail: rules})
	}
	var keys []string
	leaves("", settings, &keys)
	for _, key := range keys {
		if !handled(key) {
			p.Skipped = append(p.Skipped, Item{Key: key})
		}
	}
}

func handled(key string) bool {
	switch key {
	case "$schema", "scripts.setup", "scripts.archive", "scripts.run", "file_include_globs", "prompts.general":
		return true
	case "environment_variables.cloud":
		return false
	}
	return strings.HasPrefix(key, "scripts.run.") || strings.HasPrefix(key, "environment_variables.")
}

func leaves(prefix string, m map[string]any, out *[]string) {
	for key, value := range m {
		if prefix != "" {
			key = prefix + "." + key
		}
		if sub, ok := value.(map[string]any); ok {
			leaves(key, sub, out)
			continue
		}
		if strings.HasPrefix(key, "environment_variables.cloud.") {
			key = "environment_variables.cloud"
		}
		if !slices.Contains(*out, key) {
			*out = append(*out, key)
		}
	}
}

func (p *Plan) addStep(steps *[]string, key, command string) {
	if command == "" {
		return
	}
	for _, step := range *steps {
		if strings.TrimSpace(step) == command {
			return
		}
	}
	*steps = append(*steps, command)
	p.Added = append(p.Added, Item{Key: key, Detail: command})
	for _, name := range conductorVar.FindAllString(command, -1) {
		if !setByAO(name) {
			p.warn(key, name)
		}
	}
}

func (p *Plan) warn(key, name string) {
	item := Item{Key: key, Detail: name}
	for _, existing := range p.Warnings {
		if existing == item {
			return
		}
	}
	p.Warnings = append(p.Warnings, item)
}

func (p *Plan) addCues(scripts map[string]any) {
	if legacy := text(scripts, "run"); legacy != "" {
		p.addCue("scripts.run", "Run", legacy)
		return
	}
	actions := table(scripts, "run")
	ids := make([]string, 0, len(actions))
	for id := range actions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		key := "scripts.run." + id
		action, _ := actions[id].(map[string]any)
		command := text(action, "command")
		switch {
		case command == "":
			continue
		case action["hide"] == true:
			p.Skipped = append(p.Skipped, Item{Key: key, Detail: "hidden"})
			continue
		case !availableLocally(action["available_in"]):
			p.Skipped = append(p.Skipped, Item{Key: key, Detail: "cloud"})
			continue
		}
		if args, ok := action["args"].([]any); ok {
			for _, arg := range args {
				command += " " + shellQuote(fmt.Sprint(arg))
			}
		}
		if cwd := text(table(action, "options"), "cwd"); cwd != "" && cwd != "." {
			command = "cd -- " + shellQuote(cwd) + " && " + command
		}
		p.addCue(key, id, command)
	}
}

func availableLocally(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == "local"
	case []any:
		for _, item := range v {
			if item == "local" {
				return true
			}
		}
	}
	return false
}

func (p *Plan) addCue(key, name, command string) {
	if len(command) > maxCueCommand {
		p.Skipped = append(p.Skipped, Item{Key: key, Detail: "too long"})
		return
	}
	if runes := []rune(name); len(runes) > maxCueName {
		name = string(runes[:maxCueName])
	}
	p.Cues = append(p.Cues, CueDraft{Key: key, Name: name, Command: command})
	for _, variable := range conductorVar.FindAllString(command, -1) {
		if !setByAO(variable) {
			p.warn(key, variable)
		}
	}
}

func (p *Plan) addEnv(vars map[string]any) {
	values := map[string]string{}
	for _, layer := range []map[string]any{vars, table(vars, "local")} {
		for name, value := range layer {
			switch v := value.(type) {
			case string, int64, float64, bool:
				values[name] = fmt.Sprint(v)
			}
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var added []string
	for _, name := range names {
		key := "environment_variables." + name
		if (domain.ProjectConfig{Env: map[string]string{name: values[name]}}).Validate() != nil {
			p.Skipped = append(p.Skipped, Item{Key: key, Detail: "invalid"})
			continue
		}
		if p.hasEnv(name) {
			p.Skipped = append(p.Skipped, Item{Key: key, Detail: "already set"})
			continue
		}
		if p.Config.Env == nil {
			p.Config.Env = map[string]string{}
		}
		p.Config.Env[name] = values[name]
		added = append(added, name)
	}
	if len(added) > 0 {
		p.Added = append(p.Added, Item{Key: "environment_variables", Detail: strings.Join(added, ", ")})
	}
}

func (p *Plan) hasEnv(name string) bool {
	for existing := range p.Config.Env {
		if strings.EqualFold(existing, name) {
			return true
		}
	}
	return false
}

// copyFiles adds a Setup step, before the imported Setup, that copies the
// ignored files matching Conductor's include patterns into each workspace. The
// step finds the files when it runs and keeps a path that already exists, such
// as a project symlink.
func (p *Plan) copyFiles(ctx context.Context, repo *os.Root, root string, settings map[string]any) error {
	key, patterns := includePatterns(repo, settings)
	files, err := ignoredMatches(ctx, root, patterns)
	if err != nil {
		return err
	}
	files = slices.DeleteFunc(files, func(file string) bool { return slices.Contains(p.Config.Symlinks, file) })
	if len(files) == 0 {
		return nil
	}
	step := copyStep(patterns...)
	if slices.Contains(p.Config.PostCreate, step) {
		return nil
	}
	p.Config.PostCreate = append(p.Config.PostCreate, step)
	if len(files) > maxCopiedFiles {
		files = append(files[:maxCopiedFiles:maxCopiedFiles], fmt.Sprintf("+%d", len(files)-maxCopiedFiles))
	}
	p.Added = append(p.Added, Item{Key: key, Detail: strings.Join(files, ", ")})
	return nil
}

func includePatterns(repo *os.Root, settings map[string]any) (string, []string) {
	key, source := "files", ".env*"
	if body, err := readFile(repo, includeFile); err == nil && body != nil {
		key, source = includeFile, string(body)
	} else if globs, _ := settings["file_include_globs"].(string); strings.TrimSpace(globs) != "" {
		key, source = "file_include_globs", globs
	}
	var patterns []string
	for _, line := range strings.Split(source, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	return key, patterns
}

func copyStep(patterns ...string) string {
	specs := pathspecs(patterns)
	for i, spec := range specs {
		specs[i] = shellQuote(spec)
	}
	return `git -C "$AO_SOURCE_TREE_PATH" ls-files -z --others --ignored --exclude-standard -- ` + strings.Join(specs, " ") +
		` | xargs -0 sh -c 'for f; do [ -e "$f" ] || [ -L "$f" ] || { mkdir -p "$(dirname "$f")" && cp -pR "$AO_SOURCE_TREE_PATH/$f" "$f"; } || exit 1; done' sh`
}

func pathspecs(patterns []string) []string {
	var specs []string
	include := false
	for _, pattern := range patterns {
		magic := ":(glob)"
		switch {
		case strings.HasPrefix(pattern, "!"):
			magic, pattern = ":(exclude,glob)", pattern[1:]
		case strings.HasPrefix(pattern, `\!`), strings.HasPrefix(pattern, `\#`):
			pattern, include = pattern[1:], true
		default:
			include = true
		}
		dir := strings.HasSuffix(pattern, "/")
		pattern = strings.TrimSuffix(pattern, "/")
		if strings.Trim(pattern, "/") == "" {
			continue
		}
		if strings.Contains(pattern, "/") {
			pattern = strings.TrimPrefix(pattern, "/")
		} else {
			pattern = "**/" + pattern
		}
		if !dir {
			specs = append(specs, magic+pattern)
		}
		if !strings.HasSuffix(pattern, "/**") {
			specs = append(specs, magic+pattern+"/**")
		}
	}
	if !include {
		return nil
	}
	return specs
}

func ignoredMatches(ctx context.Context, root string, patterns []string) ([]string, error) {
	specs := pathspecs(patterns)
	if len(specs) == 0 {
		return nil, nil
	}
	args := append([]string{"-C", root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--"}, specs...)
	ignored, err := aoprocess.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, file := range strings.Split(string(ignored), "\x00") {
		if file != "" {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return files, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Key < items[j].Key })
}
