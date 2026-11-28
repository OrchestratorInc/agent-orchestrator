package nativeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// FXHome expands the invocation's FX_HOME once and makes overrides absolute.
// Relative overrides use the daemon working directory, shared by discovery and
// command construction; launches must pass the result rather than reinterpret
// the relative value inside a different workspace. fallbackHome is Scan's home.
func FXHome(env map[string]string, fallbackHome string) (string, error) {
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		home = fallbackHome
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	value, explicit := env["FX_HOME"]
	if !explicit {
		value = os.Getenv("FX_HOME")
	}
	if value = strings.TrimSpace(value); value == "" {
		return filepath.Join(home, ".fx"), nil
	}
	unresolved := false
	value = os.Expand(value, func(name string) string {
		if name == "HOME" {
			return home
		}
		v, ok := env[name]
		if !ok {
			v, ok = os.LookupEnv(name)
		}
		if !ok || v == "" {
			unresolved = true
		}
		return v
	})
	if unresolved {
		return "", errors.New("fx: FX_HOME contains an unset environment variable")
	}
	if value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "~\\") {
		value = filepath.Join(home, value[2:])
	} else if strings.HasPrefix(value, "~") {
		return "", errors.New("fx: FX_HOME requires the invocation's home, not a named user's home")
	}
	root, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return "", err
	}
	// Native FX refuses these overrides because it makes its profile private.
	if root == home || filepath.Dir(root) == root {
		return "", errors.New("fx: FX_HOME must name a private profile directory")
	}
	return root, nil
}
