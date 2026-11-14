//go:build windows

package cli

import "golang.org/x/sys/windows/registry"

// windowsRegistryInstallDirs returns the InstallLocation the NSIS installer
// recorded, per-user (HKCU) first, then per-machine (HKLM). Missing keys are
// skipped: a website install that was never run has none.
func windowsRegistryInstallDirs() []string {
	var dirs []string
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, windowsInstallRegistryKey, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if dir, _, err := k.GetStringValue("InstallLocation"); err == nil && dir != "" {
			dirs = append(dirs, dir)
		}
		_ = k.Close()
	}
	return dirs
}
