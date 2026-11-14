package modelcatalog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type openCodeLayer struct {
	model   string
	certain bool
	unknown bool
}

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

func resolveOpenCodeModel(major int, home, workingDir string, env map[string]string) string {
	var layers []openCodeLayer
	if configHome := xdgDir(home, env, "XDG_CONFIG_HOME", ".config"); configHome != "" {
		layers = append(layers, openCodeDirLayer(filepath.Join(configHome, "opencode"), true, "config.json", "opencode.json", "opencode.jsonc"))
	}
	if custom := envValue(env, "OPENCODE_CONFIG"); custom != "" {
		layers = append(layers, openCodeFileLayer(custom, false))
	}
	if openCodeProjectConfigUnknown(major, workingDir) {
		layers = append(layers, openCodeLayer{unknown: true})
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
		if layers[i].unknown {
			return ""
		}
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

func openCodeProjectConfigUnknown(major int, workingDir string) bool {
	if major >= 2 {
		return true
	}
	if workingDir == "" {
		return false
	}
	if _, _, ok := gitTopLevel(workingDir); !ok {
		return true
	}
	return repoMayHoldConfig(workingDir, "opencode.json", "opencode.jsonc", ".opencode/opencode.json", ".opencode/opencode.jsonc")
}

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
