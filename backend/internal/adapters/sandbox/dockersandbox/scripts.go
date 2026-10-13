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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sandbox bin dir: %w", err)
	}
	for _, name := range []string{"ao", "ao-enter"} {
		data, err := scripts.ReadFile("scripts/" + name)
		if err != nil {
			return err
		}
		tmp := filepath.Join(dir, "."+name+".tmp")
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return fmt.Errorf("write sandbox script %s: %w", name, err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("install sandbox script %s: %w", name, err)
		}
	}
	return nil
}
