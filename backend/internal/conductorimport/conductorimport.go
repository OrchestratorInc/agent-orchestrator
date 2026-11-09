// Package conductorimport reads a repository's Conductor settings and merges
// them into an Orchestrator project config without writing anything.
package conductorimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

// Env sets Conductor's workspace variables for Setup, Cleanup, terminals and
// Cues, so imported commands run unchanged.
func Env(rootPath, workspacePath string) map[string]string {
	return map[string]string{
		"CONDUCTOR_ROOT_PATH":      rootPath,
		"CONDUCTOR_WORKSPACE_PATH": workspacePath,
		"CONDUCTOR_WORKSPACE_NAME": filepath.Base(workspacePath),
		"CONDUCTOR_IS_LOCAL":       "1",
		"CONDUCTOR_PORT":           strconv.Itoa(port(workspacePath)),
	}
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
	_, ok := Env("", "")[name]
	return ok
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
	if root == "" {
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
	shared, err := readFile(repo, settingsFile)
	if err != nil {
		return nil, err
	}
	local, err := readFile(repo, localSettingsFile)
	if err != nil {
		return nil, err
	}
	if shared != nil || local != nil {
		settings := map[string]any{}
		for _, file := range []struct {
			name string
			body []byte
		}{{settingsFile, shared}, {localSettingsFile, local}} {
			if file.body == nil {
				continue
			}
			var layer map[string]any
			if err := toml.Unmarshal(file.body, &layer); err != nil {
				return nil, fmt.Errorf("%s: %w", file.name, err)
			}
			overlay(settings, layer)
			if plan.Source == "" {
				plan.Source = file.name
			}
		}
		return settings, nil
	}
	legacy, err := readFile(repo, legacyFile)
	if err != nil || legacy == nil {
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(legacy, &settings); err != nil {
		return nil, fmt.Errorf("%s: %w", legacyFile, err)
	}
	plan.Source = legacyFile
	return settings, nil
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

// copyFiles copies the ignored files Conductor copies into each workspace,
// before the imported Setup runs. A file the project already links stays linked.
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
	quoted := make([]string, len(files))
	for i, file := range files {
		quoted[i] = shellQuote(file)
	}
	step := `for f in ` + strings.Join(quoted, " ") + `; do if [ -e "$AO_SOURCE_TREE_PATH/$f" ]; then mkdir -p "$(dirname "$f")" && cp -pR "$AO_SOURCE_TREE_PATH/$f" "$f" || exit; fi; done`
	if slices.Contains(p.Config.PostCreate, step) {
		return nil
	}
	p.Config.PostCreate = append(p.Config.PostCreate, step)
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

func ignoredMatches(ctx context.Context, root string, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	args := []string{"-C", root, "ls-files", "-z", "--others", "--ignored"}
	for _, pattern := range patterns {
		args = append(args, "--exclude="+pattern)
	}
	matched, err := aoprocess.CommandContext(ctx, "git", args...).Output()
	if err != nil || len(matched) == 0 {
		return nil, err
	}
	check := aoprocess.CommandContext(ctx, "git", "-C", root, "check-ignore", "-z", "--stdin")
	check.Stdin = bytes.NewReader(matched)
	ignored, err := check.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, file := range strings.Split(string(ignored), "\x00") {
		if file != "" && len(files) < maxCopiedFiles {
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
