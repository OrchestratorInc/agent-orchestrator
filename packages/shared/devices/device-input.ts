// Input frames for the device hub's stream sockets, shared by the desktop
// Device tab and the phone: iOS (serve-sim) takes a one-byte tag followed by
// JSON, Android (serve-emu) plain JSON. Points are 0..1 of the screen.

export type DevicePlatform = "ios" | "android";
export type TouchPhase = "begin" | "move" | "end";
export type DeviceButton = "home" | "back" | "recents";

const IOS_TOUCH = 0x03;
const IOS_BUTTON = 0x04;

function tagged(tag: number, payload: unknown): Uint8Array<ArrayBuffer> {
	const json = new TextEncoder().encode(JSON.stringify(payload));
	const out = new Uint8Array(json.length + 1);
	out[0] = tag;
	out.set(json, 1);
	return out;
}

/**
 * A touch at (x, y). `t`, when given, is when the finger moved (the touch's
 * own timestamp, ms): the daemon replays touches at that spacing when the
 * phone's Wi-Fi hands them over in a burst, and leaves it out of what the hub
 * gets. The desktop sends none.
 */
export function encodeTouch(platform: DevicePlatform, phase: TouchPhase, x: number, y: number, t?: number): Uint8Array<ArrayBuffer> | string {
	const time = t !== undefined && t > 0 ? { t } : {};
	if (platform === "ios") return tagged(IOS_TOUCH, { type: phase, x, y, ...time });
	const action = phase === "begin" ? "down" : phase === "move" ? "move" : "up";
	return JSON.stringify({ type: "touch", action, x, y, ...time });
}

/** Encodes a hardware button, or null when the platform has none. */
export function encodeButton(platform: DevicePlatform, button: DeviceButton): Uint8Array<ArrayBuffer> | string | null {
	if (platform === "ios") {
		if (button === "back") return null;
		return tagged(IOS_BUTTON, { button: button === "recents" ? "app_switcher" : "home" });
	}
	return JSON.stringify({ type: button });
}

/** A point (x, y) inside a width × height screen, as 0..1 of it. */
export function clampPoint(x: number, y: number, width: number, height: number): { x: number; y: number } {
	const clamp = (value: number) => Math.min(1, Math.max(0, value));
	if (width <= 0 || height <= 0) return { x: 0, y: 0 };
	return { x: clamp(x / width), y: clamp(y / height) };
}

/** A pointer's client position over an element's box, as 0..1 of it. */
export function normalizedPoint(rect: { left: number; top: number; width: number; height: number }, clientX: number, clientY: number): { x: number; y: number } {
	return clampPoint(clientX - rect.left, clientY - rect.top, rect.width, rect.height);
}
