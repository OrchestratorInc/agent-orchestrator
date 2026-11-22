// Input frames for the device hub's stream sockets. iOS (serve-sim) takes a
// one-byte tag followed by JSON; Android (serve-emu) takes plain JSON. Points
// are normalized to the displayed screen, 0..1 on each axis.

export type DevicePlatform = "ios" | "android";
export type TouchPhase = "begin" | "move" | "end";
export type DeviceButton = "home" | "back" | "recents";

const IOS_TOUCH = 0x03;
const IOS_BUTTON = 0x04;

const encoder = new TextEncoder();

function tagged(tag: number, payload: unknown): Uint8Array<ArrayBuffer> {
	const json = encoder.encode(JSON.stringify(payload));
	const out = new Uint8Array(json.length + 1);
	out[0] = tag;
	out.set(json, 1);
	return out;
}

export function encodeTouch(platform: DevicePlatform, phase: TouchPhase, x: number, y: number): Uint8Array<ArrayBuffer> | string {
	if (platform === "ios") return tagged(IOS_TOUCH, { type: phase, x, y });
	const action = phase === "begin" ? "down" : phase === "move" ? "move" : "up";
	return JSON.stringify({ type: "touch", action, x, y });
}

/** Encodes a hardware button, or null when the platform has none. */
export function encodeButton(platform: DevicePlatform, button: DeviceButton): Uint8Array<ArrayBuffer> | string | null {
	if (platform === "ios") {
		if (button === "back") return null;
		return tagged(IOS_BUTTON, { button: button === "recents" ? "app_switcher" : "home" });
	}
	return JSON.stringify({ type: button });
}

export function normalizedPoint(rect: { left: number; top: number; width: number; height: number }, clientX: number, clientY: number): { x: number; y: number } {
	const clamp = (value: number) => Math.min(1, Math.max(0, value));
	if (rect.width <= 0 || rect.height <= 0) return { x: 0, y: 0 };
	return { x: clamp((clientX - rect.left) / rect.width), y: clamp((clientY - rect.top) / rect.height) };
}

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
