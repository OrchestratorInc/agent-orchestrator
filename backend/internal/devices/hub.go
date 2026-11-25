package devices

import (
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// HubRouteAllowed reports whether a client may reach this hub path for the
// leased device. Only the stream, its input socket and screenshots are
// forwarded, and only for the session's own device: the hub's dashboard,
// shell-exec and device-management routes never leave the host.
func HubRouteAllowed(device ports.DeviceInfo, tail, queryDevice string) bool {
	if !deviceIDPattern.MatchString(device.ID) {
		return false
	}
	switch device.Platform {
	case ports.DevicePlatformIOS:
		helper := "vendor/serve-sim/helper/" + device.ID + "/"
		switch tail {
		case helper + "stream.mjpeg", helper + "stream.avcc", helper + "config", helper + "ws":
			return true
		case "vendor/serve-sim/helper/ws":
			return queryDevice == device.ID
		}
	case ports.DevicePlatformAndroid:
		switch tail {
		case "vendor/serve-emu/ws", "vendor/serve-emu/api/screenshot":
			return queryDevice == device.ID
		}
	}
	return false
}
