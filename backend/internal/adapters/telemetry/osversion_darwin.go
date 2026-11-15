//go:build darwin

package telemetry

import "golang.org/x/sys/unix"

func osVersion() string {
	v, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	return v
}
