package modelcatalog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// openCodeLayer is one OpenCode config source that may set the root model.
// certain is false when AO cannot know whether the layer applies to the
// session it will launch; if such a layer is the one that wins, the default is
// left unresolved rather than guessed.
type openCodeLayer struct {
	model   string
	certain bool
}

// openCodeManagedConfigDirs lists the system directories OpenCode reads
// managed (administrator) config from. Managed config outranks every user
// layer. A package variable so tests can point it at a temp dir.
var openCodeManagedConfigDirs = func() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Library/Application Support/opencode"}
	case "windows":
		if programData := strings.TrimSpace(os.Getenv("ProgramData")); programData != "" {
			return []string{filepath.Join(programData, "opencode")}
		}
		return nil
	default:
		return []string{"/etc/opencode"}
	}
}

// resolveOpenCodeModel returns the root model OpenCode will run for a session
// launched from workingDir's repository, or "" when that cannot be determined.
//
// Both majors layer config lowest to highest as: global, OPENCODE_CONFIG,
// project opencode.json(c) from the outermost directory inward, then
// .opencode/opencode.json(c) the same way (every .opencode file overrides every
// direct file), then OPENCODE_CONFIG_CONTENT and managed config. They differ in
// how far up the project search goes: v1 stops at the git root, v2 continues to
// the filesystem root.
//
// Discovery runs against the project checkout, but sessions launch from AO
// worktrees elsewhere on disk, so only layers that are identical for both are
// trusted:
//   - in-repository files are the same in the checkout and every worktree;
//   - v2 directories above the git root differ, so they are uncertain;
//   - the user's OPENCODE_CONFIG is replaced by AO's own file at TUI launch but
//     kept for ACP sessions, and OPENCODE_CONFIG_DIR's contents are not
//     modeled, so both are uncertain.
//
// Remote (organization) config is the lowest layer and is not visible here; it
// can only matter when no visible layer sets a model, which already resolves
// to "".
//
// https://dev.opencode.ai/docs/config/ (v1), https://opencode.ai/v2/docs/config (v2)
func resolveOpenCodeModel(major int, home, workingDir string, env map[string]string) string {
	var layers []openCodeLayer
	if configHome := xdgDir(home, env, "XDG_CONFIG_HOME", ".config"); configHome != "" {
		layers = append(layers, openCodeDirLayer(filepath.Join(configHome, "opencode"), true, "config.json", "opencode.json", "opencode.jsonc"))
	}
	if custom := envValue(env, "OPENCODE_CONFIG"); custom != "" {
		layers = append(layers, openCodeFileLayer(custom, false))
	}

	projectDirs := openCodeProjectDirs(major, workingDir)
	for _, dir := range projectDirs {
		layers = append(layers, openCodeDirLayer(dir.path, dir.certain, "opencode.json", "opencode.jsonc"))
	}
	for _, dir := range projectDirs {
		layers = append(layers, openCodeDirLayer(filepath.Join(dir.path, ".opencode"), dir.certain, "opencode.json", "opencode.jsonc"))
	}

	if configDir := envValue(env, "OPENCODE_CONFIG_DIR"); configDir != "" {
		layers = append(layers, openCodeDirLayer(configDir, false, "opencode.json", "opencode.jsonc"))
	}
	if content := envValue(env, "OPENCODE_CONFIG_CONTENT"); content != "" {
		layers = append(layers, openCodeLayer{model: strings.TrimSpace(parseJSONCModelKey([]byte(content))), certain: true})
	}
	for _, dir := range openCodeManagedConfigDirs() {
		layers = append(layers, openCodeDirLayer(dir, true, "opencode.json", "opencode.jsonc"))
	}

	for i := len(layers) - 1; i >= 0; i-- {
		if layers[i].model == "" {
			continue
		}
		if !layers[i].certain {
			return ""
		}
		return layers[i].model
	}
	return ""
}

type openCodeProjectDir struct {
	path    string
	certain bool
}

// openCodeProjectDirs lists the directories OpenCode searches for project
// config, outermost first. Directories inside the repository are certain.
// Above the git root, v1 reads nothing and v2 reads directories that depend on
// where the session is launched. Without a git root the search bound is not
// known, so every directory is uncertain.
func openCodeProjectDirs(major int, workingDir string) []openCodeProjectDir {
	if workingDir == "" {
		return nil
	}
	start, err := filepath.Abs(workingDir)
	if err != nil {
		return nil
	}
	var chain []string
	for dir := start; ; {
		chain = append(chain, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	gitRoot := -1
	for i, dir := range chain {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			gitRoot = i
			break
		}
	}
	var dirs []openCodeProjectDir
	for i := len(chain) - 1; i >= 0; i-- {
		inRepo := gitRoot >= 0 && i <= gitRoot
		if !inRepo && major < 2 && gitRoot >= 0 {
			continue
		}
		dirs = append(dirs, openCodeProjectDir{path: chain[i], certain: inRepo})
	}
	return dirs
}

// openCodeDirLayer reads the named config files in one directory. The order in
// which OpenCode merges sibling .json and .jsonc files is not documented, so
// siblings that disagree make the layer uncertain.
func openCodeDirLayer(dir string, certain bool, names ...string) openCodeLayer {
	layer := openCodeLayer{certain: certain}
	for _, name := range names {
		model := openCodeFileLayer(filepath.Join(dir, name), certain).model
		if model == "" {
			continue
		}
		if layer.model != "" && !strings.EqualFold(layer.model, model) {
			layer.certain = false
		}
		layer.model = model
	}
	return layer
}

func openCodeFileLayer(path string, certain bool) openCodeLayer {
	raw, err := readModelConfig(path)
	if err != nil {
		return openCodeLayer{certain: certain}
	}
	return openCodeLayer{model: strings.TrimSpace(parseJSONCModelKey(raw)), certain: certain}
}
