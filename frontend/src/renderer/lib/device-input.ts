// Input frames for the device hub's stream sockets live in packages/shared,
// so the desktop and the phone cannot drift apart.

import type { DevicePlatform } from "../../../../packages/shared/devices/device-input";

export { encodeButton, encodeTouch, normalizedPoint, type DeviceButton, type DevicePlatform, type TouchPhase } from "../../../../packages/shared/devices/device-input";

/** The stream and input routes the daemon forwards for a leased device. */
export function deviceStreamRoutes(apiBaseUrl: string, sessionId: string, platform: DevicePlatform, deviceId: string) {
	const hub = `${apiBaseUrl}/api/v1/sessions/${encodeURIComponent(sessionId)}/device/hub`;
	const ws = hub.replace(/^http/, "ws");
	const device = encodeURIComponent(deviceId);
	// iOS: the MJPEG stream without the daemon's repeated frame (the canvas paints
	// every part). Android: the H.264 socket, with screenshots where WebCodecs is
	// missing.
	return platform === "ios"
		? { frames: `${hub}/vendor/serve-sim/helper/${device}/stream.mjpeg?repeat=0`, video: null, input: `${ws}/vendor/serve-sim/helper/ws?device=${device}` }
		: { frames: `${apiBaseUrl}/api/v1/sessions/${encodeURIComponent(sessionId)}/device/screenshot`, video: `${ws}/vendor/serve-emu/ws?device=${device}&frame-meta=1`, input: `${ws}/vendor/serve-emu/ws?device=${device}&video=0` };
}
