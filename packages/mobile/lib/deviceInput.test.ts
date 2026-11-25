import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import { clampPoint, deviceAvccURL, deviceInputURL, deviceScreenshotRequest, missingPlatforms, deviceMjpegURL, deviceVideoURL, emptyFrameLayers, encodeButton, encodeTouch, frameLoaded, withFrame } from "./deviceInput";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

const cfg: ServerConfig = { host: "192.168.1.20", httpPort: "3011", muxPort: "14801", secure: false, password: "pw" };

const decode = (frame: ArrayBuffer | string | null) => {
	if (!(frame instanceof ArrayBuffer)) throw new Error("expected a binary frame");
	const bytes = new Uint8Array(frame);
	return { tag: bytes[0], payload: JSON.parse(new TextDecoder().decode(bytes.subarray(1))) };
};

describe("device input", () => {
	it("speaks the hub's iOS and Android input protocols", () => {
		expect(decode(encodeTouch("ios", "begin", 0.5, 0.25))).toEqual({ tag: 0x03, payload: { type: "begin", x: 0.5, y: 0.25 } });
		expect(decode(encodeButton("ios", "recents"))).toEqual({ tag: 0x04, payload: { button: "app_switcher" } });
		expect(encodeButton("ios", "back")).toBeNull();
		expect(JSON.parse(encodeTouch("android", "end", 0.1, 0.2) as string)).toEqual({ type: "touch", action: "up", x: 0.1, y: 0.2 });
		// The touch's own time rides along for the daemon to pace by.
		expect(decode(encodeTouch("ios", "move", 0.5, 0.25, 1234.5))).toEqual({ tag: 0x03, payload: { type: "move", x: 0.5, y: 0.25, t: 1234.5 } });
		expect(JSON.parse(encodeTouch("android", "move", 0.1, 0.2, 99) as string)).toEqual({ type: "touch", action: "move", x: 0.1, y: 0.2, t: 99 });
		expect(JSON.parse(encodeButton("android", "back") as string)).toEqual({ type: "back" });
	});

	it("clamps touch points to the screen", () => {
		expect(clampPoint(50, 100, 100, 200)).toEqual({ x: 0.5, y: 0.5 });
		expect(clampPoint(-5, 500, 100, 200)).toEqual({ x: 0, y: 1 });
		expect(clampPoint(1, 1, 0, 0)).toEqual({ x: 0, y: 0 });
	});

	it("reaches the session's device through the paired daemon with its credential", () => {
		expect(deviceInputURL(cfg, "ao-1", "ios", "UDID-1")).toBe("ws://192.168.1.20:3011/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/ws?device=UDID-1&pace=1");
		expect(deviceInputURL(cfg, "ao-1", "android", "emulator-5554")).toBe("ws://192.168.1.20:3011/api/v1/sessions/ao-1/device/hub/vendor/serve-emu/ws?device=emulator-5554&video=0&pace=1");
		const request = deviceScreenshotRequest({ ...cfg, secure: true }, "ao-1");
		expect(request.url).toBe("https://192.168.1.20:3011/api/v1/sessions/ao-1/device/screenshot");
		expect(request.headers.Authorization).toBe("Bearer pw");
	});
});

describe("frame layers", () => {
	it("puts the first frame in the front layer", () => {
		const layers = withFrame(emptyFrameLayers, "a");
		expect(layers).toEqual({ uris: ["a", null], front: 0 });
	});

	it("loads the next frame behind and only brings it forward once loaded", () => {
		let layers = withFrame(emptyFrameLayers, "a");
		layers = withFrame(layers, "b");
		expect(layers).toEqual({ uris: ["a", "b"], front: 0 });
		layers = frameLoaded(layers, 1);
		expect(layers.front).toBe(1);
		layers = withFrame(layers, "c");
		expect(layers).toEqual({ uris: ["c", "b"], front: 1 });
	});

	it("ignores a frame that is already showing or loading, and a load of an empty layer", () => {
		const layers = withFrame(withFrame(emptyFrameLayers, "a"), "b");
		expect(withFrame(layers, "a")).toBe(layers);
		expect(withFrame(layers, "b")).toBe(layers);
		expect(frameLoaded(withFrame(emptyFrameLayers, "a"), 1).front).toBe(0);
	});
});

describe("device video url", () => {
	it("points at the simulator's MJPEG stream through the daemon", () => {
		expect(deviceMjpegURL(cfg, "ao-1", "UDID-1")).toBe("http://192.168.1.20:3011/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/UDID-1/stream.mjpeg?repeat=0");
		expect(deviceAvccURL(cfg, "ao-1", "UDID-1")).toBe("http://192.168.1.20:3011/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/UDID-1/stream.avcc");
	});

	it("asks the daemon for the emulator's framed H.264 stream", () => {
		expect(deviceVideoURL(cfg, "ao-1", "emulator-5554")).toBe("ws://192.168.1.20:3011/api/v1/sessions/ao-1/device/hub/vendor/serve-emu/ws?device=emulator-5554&frame-meta=1");
	});
});

describe("missingPlatforms", () => {
	it("says what each platform this machine cannot run is missing, one line each", () => {
		expect(missingPlatforms([
			{ platform: "ios", available: false, reason: "Install Xcode." },
			{ platform: "android", available: false, reason: "Install the Android SDK." },
		])).toBe("iOS: Install Xcode.\nAndroid: Install the Android SDK.");
	});

	it("is empty when both run, and names only the missing one otherwise", () => {
		expect(missingPlatforms([{ platform: "ios", available: true }, { platform: "android", available: true }])).toBe("");
		expect(missingPlatforms([{ platform: "ios", available: true }, { platform: "android", available: false }])).toBe("Android: Not available.");
	});
});

