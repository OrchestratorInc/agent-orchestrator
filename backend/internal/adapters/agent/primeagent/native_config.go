package primeagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// NativeSessionConfigDir identifies the exact native session store selected by
// the launch environment. Imported restores pin PRIME_AGENT_SESSION_DIR.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value := func(key string) string {
		if v, set := env[key]; set {
			return v
		}
		return os.Getenv(key)
	}
	home := value("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	expand := func(path string) string {
		if path == "~" {
			return home
		}
		if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
			return filepath.Join(home, path[2:])
		}
		return path
	}
	root, set := env["PRIME_AGENT_SESSION_DIR"]
	if !set {
		root, set = os.LookupEnv("PRIME_AGENT_SESSION_DIR")
	}
	if !set {
		root = value("PRIME_AGENT_CODING_AGENT_SESSION_DIR")
	}
	if root != "" {
		return filepath.Abs(expand(root))
	}
	profile := value(primeAgentCodingAgentDirEnv)
	if profile == "" {
		profile = filepath.Join(home, ".prime", "agent")
	}
	return filepath.Abs(filepath.Join(expand(profile), "sessions"))
}
