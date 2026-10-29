package providerkeys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Publish updates the systemd user manager now and its environment.d source
// for later logins. This reaches processes started by that manager; a shell
// started by another parent continues to inherit that parent's environment.
func Publish(ctx context.Context, name, value string) error {
	if !validName(name) || !validValue(value) || value == "" {
		return errors.New("invalid provider credential")
	}
	cmd := process.CommandContext(ctx, "systemctl", "--user", "import-environment", name)
	cmd.Env = append(os.Environ(), name+"="+value)
	if err := cmd.Run(); err != nil {
		return err
	}
	dir, path, err := environmentFile(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("user environment file is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// environment.d expands $, even inside quotes. Double it before writing.
	escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "$$").Replace(value)
	data := []byte(name + `="` + escaped + "\"\n")
	tmp, err := os.CreateTemp(dir, ".ao-env-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Unpublish removes name from the systemd user manager and deletes AO's
// environment.d source for it. Processes that already inherited it keep it.
func Unpublish(ctx context.Context, name string) error {
	if !validName(name) {
		return errors.New("invalid provider credential")
	}
	if err := process.CommandContext(ctx, "systemctl", "--user", "unset-environment", name).Run(); err != nil {
		return err
	}
	_, path, err := environmentFile(name)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("user environment file is not a regular file")
	}
	return os.Remove(path)
}

func environmentFile(name string) (dir, path string, err error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", errors.New("user configuration home is unavailable")
	}
	configHome := filepath.Join(home, ".config")
	if configured := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(configured) {
		configHome = configured
	}
	dir = filepath.Join(configHome, "environment.d")
	return dir, filepath.Join(dir, "90-ao-"+name+".conf"), nil
}
