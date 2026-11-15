package termtheme

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	FileName = "terminal-theme"

	EnvTheme     = "TERM_THEME"
	EnvColorFgBg = "COLORFGBG"
)

type Scheme string

const (
	SchemeDark  Scheme = "dark"
	SchemeLight Scheme = "light"
)

func Read(dataDir string) (Scheme, bool) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, FileName))
	if err != nil {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(string(raw))) {
	case string(SchemeLight):
		return SchemeLight, true
	case string(SchemeDark):
		return SchemeDark, true
	default:
		return "", false
	}
}

func Apply(env map[string]string, dataDir string) {
	ApplyFoldingKeys(env, dataDir, runtime.GOOS == "windows")
}

func ApplyFoldingKeys(env map[string]string, dataDir string, foldKeys bool) {
	if env == nil {
		return
	}
	scheme, ok := Read(dataDir)
	if !ok {
		return
	}
	if !hasValue(env, EnvTheme, foldKeys) {
		env[EnvTheme] = string(scheme)
	}
	if !hasValue(env, EnvColorFgBg, foldKeys) {
		env[EnvColorFgBg] = colorFgBg(scheme)
	}
}

func hasValue(env map[string]string, key string, foldKeys bool) bool {
	if !foldKeys {
		return strings.TrimSpace(env[key]) != ""
	}
	found := false
	for existing, value := range env {
		if !strings.EqualFold(existing, key) {
			continue
		}
		if strings.TrimSpace(value) != "" {
			found = true
			continue
		}
		delete(env, existing)
	}
	return found
}

func colorFgBg(scheme Scheme) string {
	if scheme == SchemeLight {
		return "0;15"
	}
	return "15;0"
}
