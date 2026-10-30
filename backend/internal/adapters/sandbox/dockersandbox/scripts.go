package dockersandbox

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed scripts/ao scripts/ao-enter
var scripts embed.FS

// InstallBin writes the in-container helper scripts into dir. The daemon
// rewrites them on every Ensure so an upgraded AO never runs stale scripts.
func InstallBin(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("sandbox bin dir: %w", err)
	}
	for _, name := range []string{"ao", "ao-enter"} {
		data, err := scripts.ReadFile("scripts/" + name)
		if err != nil {
			return err
		}
		if err := writeScript(dir, name, data); err != nil {
			return err
		}
	}
	return nil
}

// writeScript installs one script via a uniquely named temp file so concurrent
// installs into the same dir never share a temp path. Chmod runs after create
// so the mode does not depend on the process umask.
func writeScript(dir, name string, data []byte) (err error) {
	f, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return fmt.Errorf("write sandbox script %s: %w", name, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write sandbox script %s: %w", name, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("write sandbox script %s: %w", name, err)
	}
	if err = os.Chmod(tmp, 0o700); err != nil { //nolint:gosec // helper scripts must be executable; 0700 is owner-only and the container runs as the host uid:gid.
		return fmt.Errorf("chmod sandbox script %s: %w", name, err)
	}
	if err = os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("install sandbox script %s: %w", name, err)
	}
	return nil
}
