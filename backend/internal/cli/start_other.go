//go:build !windows

package cli

// windowsRegistryInstallDirs has no registry to read off Windows.
func windowsRegistryInstallDirs() []string { return nil }
