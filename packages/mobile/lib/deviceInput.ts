import { authHeaders, httpBase, type ServerConfig } from "./config";

// Input frames for the device hub's stream sockets, the same protocol the
// desktop Device tab speaks: iOS (serve-sim) takes a one-byte tag followed by
// JSON, Android (serve-emu) plain JSON. Points are 0..1 of the screen.

export type DevicePlatform = "ios" | "android";
export type TouchPhase = "begin" | "move" | "end";
export type DeviceButton = "home" | "back" | "recents";

const IOS_TOUCH = 0x03;
const IOS_BUTTON = 0x04;

function tagged(tag: number, payload: unknown): ArrayBuffer {
	const json = new TextEncoder().encode(JSON.stringify(payload));
	const out = new Uint8Array(json.length + 1);
	out[0] = tag;
	out.set(json, 1);
	return out.buffer;
}

/**
 * `t` is when the finger moved (the touch's own timestamp, ms): the daemon
 * replays touches at that spacing when the phone's Wi-Fi hands them over in a
 * burst, and leaves it out of what the hub gets.
 */
export function encodeTouch(platform: DevicePlatform, phase: TouchPhase, x: number, y: number, t?: number): ArrayBuffer | string {
	const time = t !== undefined && t > 0 ? { t } : {};
	if (platform === "ios") return tagged(IOS_TOUCH, { type: phase, x, y, ...time });
	const action = phase === "begin" ? "down" : phase === "move" ? "move" : "up";
	return JSON.stringify({ type: "touch", action, x, y, ...time });
}

/** Encodes a hardware button, or null when the platform has none. */
export function encodeButton(platform: DevicePlatform, button: DeviceButton): ArrayBuffer | string | null {
	if (platform === "ios") {
		if (button === "back") return null;
		return tagged(IOS_BUTTON, { button: button === "recents" ? "app_switcher" : "home" });
	}
	return JSON.stringify({ type: button });
}

export function clampPoint(x: number, y: number, width: number, height: number): { x: number; y: number } {
	const clamp = (value: number) => Math.min(1, Math.max(0, value));
	if (width <= 0 || height <= 0) return { x: 0, y: 0 };
	return { x: clamp(x / width), y: clamp(y / height) };
}

/** The daemon's screenshot route for the session's device, and the headers it needs. */
export function deviceScreenshotRequest(cfg: ServerConfig, sessionId: string) {
	return {
		url: `${httpBase(cfg)}/api/v1/sessions/${encodeURIComponent(sessionId)}/device/screenshot`,
		headers: authHeaders(cfg),
	};
}

/**
 * One frame of the session's device as a data URI. The phone fetches frames
 * itself rather than handing the URL and credentials to <Image>: the app's
 * fetch is the path every other authenticated request takes, and <Image>
 * with custom headers never issued a request on Android.
 */
export async function fetchDeviceFrame(cfg: ServerConfig, sessionId: string, signal?: AbortSignal): Promise<string> {
	const request = deviceScreenshotRequest(cfg, sessionId);
	const res = await fetch(request.url, { headers: request.headers, signal });
	if (!res.ok) throw new Error(`device frame ${res.status}`);
	const blob = await res.blob();
	return await new Promise<string>((resolve, reject) => {
		const reader = new FileReader();
		reader.onloadend = () => (typeof reader.result === "string" ? resolve(reader.result) : reject(new Error("device frame unreadable")));
		reader.onerror = () => reject(reader.error ?? new Error("device frame unreadable"));
		reader.readAsDataURL(blob);
	});
}

/** The device hub's input socket for the leased device, through the daemon. */
export function deviceInputURL(cfg: ServerConfig, sessionId: string, platform: DevicePlatform, deviceId: string): string {
	const hub = `${httpBase(cfg).replace(/^http/, "ws")}/api/v1/sessions/${encodeURIComponent(sessionId)}/device/hub`;
	const device = encodeURIComponent(deviceId);
	// Android's socket also carries video unless it is told not to; this one is
	// for touches only. pace=1 asks the daemon to replay touches at the spacing
	// they were made (see encodeTouch).
	return platform === "ios" ? `${hub}/vendor/serve-sim/helper/ws?device=${device}&pace=1` : `${hub}/vendor/serve-emu/ws?device=${device}&video=0&pace=1`;
}

/** The daemon's WebSocket for an Android emulator's H.264 video (frames behind the hub's SEMU header). */
export function deviceVideoURL(cfg: ServerConfig, sessionId: string, deviceId: string): string {
	const hub = `${httpBase(cfg).replace(/^http/, "ws")}/api/v1/sessions/${encodeURIComponent(sessionId)}/device/hub`;
	return `${hub}/vendor/serve-emu/ws?device=${encodeURIComponent(deviceId)}&frame-meta=1`;
}

/**
 * The daemon's route for an iOS Simulator's H.264 stream (length-prefixed
 * packets over HTTP): a scrolling simulator in ~5 Mbit/s where MJPEG takes ~112.
 */
export function deviceAvccURL(cfg: ServerConfig, sessionId: string, deviceId: string): string {
	return `${httpBase(cfg)}/api/v1/sessions/${encodeURIComponent(sessionId)}/device/hub/vendor/serve-sim/helper/${encodeURIComponent(deviceId)}/stream.avcc`;
}

/**
 * The daemon's route for an iOS Simulator's MJPEG stream (multipart JPEG frames
 * over HTTP). repeat=0 turns off the repeated frame the daemon adds for browsers.
 */
export function deviceMjpegURL(cfg: ServerConfig, sessionId: string, deviceId: string): string {
	return `${httpBase(cfg)}/api/v1/sessions/${encodeURIComponent(sessionId)}/device/hub/vendor/serve-sim/helper/${encodeURIComponent(deviceId)}/stream.mjpeg?repeat=0`;
}

/** Headers for the device sockets: the loopback Origin the daemon's CORS guard accepts, and the pairing credential. */
export function deviceSocketHeaders(cfg: ServerConfig): Record<string, string> {
	return { Origin: "http://localhost", ...authHeaders(cfg) };
}

/**
 * Opens the input socket. React Native's WebSocket sets Origin to the daemon's
 * address, which the daemon's CORS guard refuses; pin the loopback Origin the
 * mux uses, with the pairing credential.
 */
export function openDeviceInput(cfg: ServerConfig, url: string): WebSocket {
	const NativeSocket = WebSocket as unknown as {
		new (url: string, protocols?: string | string[], options?: { headers?: Record<string, string> }): WebSocket;
	};
	const socket = new NativeSocket(url, undefined, { headers: deviceSocketHeaders(cfg) });
	socket.binaryType = "arraybuffer";
	return socket;
}

/**
 * Two image layers take turns showing the device screen. Changing one <Image>'s
 * source blanks it on Android until the new bitmap has decoded, which showed as
 * a black flash every few frames. A new frame instead loads into the layer
 * that is not showing and takes over once it has loaded; the old frame stays up
 * until then.
 */
export type FrameLayers = { uris: [string | null, string | null]; front: 0 | 1 };

export const emptyFrameLayers: FrameLayers = { uris: [null, null], front: 0 };

/** Where a newly fetched frame goes; the same layers when the frame is already showing or loading. */
export function withFrame(layers: FrameLayers, uri: string): FrameLayers {
	const { uris, front } = layers;
	if (uris[0] === uri || uris[1] === uri) return layers;
	// The first frame fills the front layer; later ones load behind it.
	const target: 0 | 1 = uris[front] === null ? front : front === 0 ? 1 : 0;
	const next: [string | null, string | null] = [uris[0], uris[1]];
	next[target] = uri;
	return { uris: next, front };
}

/** A layer finished loading its frame: it comes to the front. */
export function frameLoaded(layers: FrameLayers, layer: 0 | 1): FrameLayers {
	return layers.front === layer || layers.uris[layer] === null ? layers : { ...layers, front: layer };
}

/**
 * Why this machine cannot run each platform it cannot run (Xcode, the Android
 * SDK, Node missing), one line each as the daemon words it; empty when both run.
 */
export function missingPlatforms(platforms: readonly { platform: "ios" | "android"; available: boolean; reason?: string }[]): string {
	return platforms
		.filter((platform) => !platform.available)
		.map((platform) => `${platform.platform === "ios" ? "iOS" : "Android"}: ${platform.reason ?? "Not available."}`)
		.join("\n");
}
