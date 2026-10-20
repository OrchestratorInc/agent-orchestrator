//go:build windows

package procinventory

import (
	"context"
)

// signalGroup is unreachable on Windows: Scan always fails first.
func signalGroup(pid int, sig Signal) error {
	return ErrScanUnsupported
}

// defaultProbe has no portable implementation on Windows.
func defaultProbe(pid int) bool {
	return false
}

// SystemScan snapshots the full process table. Unimplemented on Windows: the
// orphan problem this package solves is launchd-adoption specific (macOS).
func SystemScan(ctx context.Context) ([]Entry, error) {
	return nil, ErrScanUnsupported
}
