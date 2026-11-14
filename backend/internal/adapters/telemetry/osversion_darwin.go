//go:build darwin

package telemetry

import "golang.org/x/sys/unix"

// osVersion returns the macOS product version (15.1.2), not the Darwin kernel
// release, matching what posthog-js reports for the renderer.
func osVersion() string {
	v, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	return v
}
