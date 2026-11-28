import { describe, expect, it } from "vitest";
import { deviceStreamRoutes, encodeButton, encodeTouch, normalizedPoint } from "./device-input";

const decode = (frame: Uint8Array | string | null) => {
	if (!(frame instanceof Uint8Array)) throw new Error("expected a binary frame");
	return { tag: frame[0], payload: JSON.parse(new TextDecoder().decode(frame.subarray(1))) };
};

describe("device input", () => {
	it("tags iOS touches and buttons", () => {
		expect(decode(encodeTouch("ios", "begin", 0.25, 0.5))).toEqual({ tag: 0x03, payload: { type: "begin", x: 0.25, y: 0.5 } });
		expect(decode(encodeButton("ios", "home"))).toEqual({ tag: 0x04, payload: { button: "home" } });
		expect(decode(encodeButton("ios", "recents"))).toEqual({ tag: 0x04, payload: { button: "app_switcher" } });
		expect(encodeButton("ios", "back")).toBeNull();
	});

	it("sends Android touches and buttons as JSON", () => {
		expect(JSON.parse(encodeTouch("android", "move", 0.1, 0.9) as string)).toEqual({ type: "touch", action: "move", x: 0.1, y: 0.9 });
		expect(JSON.parse(encodeTouch("android", "end", 0, 0) as string).action).toBe("up");
		expect(JSON.parse(encodeButton("android", "back") as string)).toEqual({ type: "back" });
	});

	it("normalizes and clamps points to the screen", () => {
		const rect = { left: 10, top: 20, width: 200, height: 400 };
		expect(normalizedPoint(rect, 110, 220)).toEqual({ x: 0.5, y: 0.5 });
		expect(normalizedPoint(rect, -50, 900)).toEqual({ x: 0, y: 1 });
		expect(normalizedPoint({ left: 0, top: 0, width: 0, height: 0 }, 5, 5)).toEqual({ x: 0, y: 0 });
	});

	it("builds the daemon's stream routes for the leased device", () => {
		expect(deviceStreamRoutes("http://127.0.0.1:3001", "ao-1", "ios", "UDID-1")).toEqual({
			frames: "http://127.0.0.1:3001/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/UDID-1/stream.mjpeg?repeat=0",
			video: null,
			input: "ws://127.0.0.1:3001/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/ws?device=UDID-1",
		});
		// Android frames are the daemon's screenshot route: the hub's own is POST-only.
		expect(deviceStreamRoutes("http://127.0.0.1:3001", "ao-1", "android", "emulator-5554").video).toBe(
			"ws://127.0.0.1:3001/api/v1/sessions/ao-1/device/hub/vendor/serve-emu/ws?device=emulator-5554&frame-meta=1",
		);
		expect(deviceStreamRoutes("http://127.0.0.1:3001", "ao-1", "android", "emulator-5554").frames).toBe(
			"http://127.0.0.1:3001/api/v1/sessions/ao-1/device/screenshot",
		);
		expect(deviceStreamRoutes("http://127.0.0.1:3001", "ao-1", "android", "emulator-5554").input).toBe(
			"ws://127.0.0.1:3001/api/v1/sessions/ao-1/device/hub/vendor/serve-emu/ws?device=emulator-5554&video=0",
		);
	});
});
