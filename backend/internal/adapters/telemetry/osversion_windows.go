//go:build windows

package telemetry

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// osVersion returns major.minor.build (10.0.22631). RtlGetVersion is not
// subject to the manifest-based version lie GetVersionEx tells.
func osVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
