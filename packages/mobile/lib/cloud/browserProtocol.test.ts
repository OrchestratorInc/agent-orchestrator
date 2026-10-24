import { afterEach, describe, expect, it, vi } from "vitest";
import { cloudBrowserCommand, cloudBrowserHello, decodeCloudBrowserFrame, initialCloudBrowserState, projectCloudBrowserMessage } from "./browserProtocol";

// Hand-built SOF0 JPEG: 640 × 360; metadata deliberately reports another size.
const jpeg = Uint8Array.from([255, 216, 255, 192, 0, 11, 8, 1, 104, 2, 128, 1, 1, 17, 0, 255, 217]);
function packet(header: object, image = jpeg): ArrayBuffer {
	const json = new TextEncoder().encode(JSON.stringify(header));
	const bytes = new Uint8Array(4 + json.length + image.length);
	new DataView(bytes.buffer).setUint32(0, json.length);
	bytes.set(json, 4); bytes.set(image, 4 + json.length);
	return bytes.buffer;
}
afterEach(() => vi.unstubAllGlobals());

describe("Cloud browser protocol", () => {
	it("uses the Cloud hello and decodes still frames without screencast metadata", () => {
		expect(cloudBrowserHello()).toEqual({ t: "hello", w: 1280, h: 800, dpr: 1, quality: 70, scrollQuality: 70, scrollScale: 1, maxInFlight: 4, embed: false });
		expect(decodeCloudBrowserFrame(packet({ seq: 7, ts: 123 }))).toMatchObject({ sequence: 7n, capturedAtMs: 123n, width: 640, height: 360 });
		expect(decodeCloudBrowserFrame(packet({ seq: 8, ts: 124, meta: { deviceWidth: 900, deviceHeight: 700 }, crop: [600, 300] }))).toMatchObject({ width: 640, height: 360, crop: { width: 600, height: 300 } });
	});

	it("rejects malformed envelopes, truncated JPEGs and crops outside the capture", () => {
		const length = packet({ seq: 1, ts: 1 }); new DataView(length).setUint32(0, 0xffffffff);
		for (const frame of [new ArrayBuffer(3), length, packet({ seq: -1, ts: 1 }), packet({ seq: 1, ts: 1 }, jpeg.slice(0, -1)), packet({ seq: 1, ts: 1, crop: [900, 300] }), packet({ seq: 1, ts: 1, crop: [0, 300] })]) expect(() => decodeCloudBrowserFrame(frame)).toThrow();
		const invalid = packet({ seq: 1, ts: 1 }); new Uint8Array(invalid)[4] = 0xff;
		expect(() => decodeCloudBrowserFrame(invalid)).toThrow();
	});
	it("decodes bounded UTF-8 headers on native runtimes without TextDecoder", () => {
		const frame = packet({ seq: 9, ts: 123, meta: { description: "café" } });
		vi.stubGlobal("TextDecoder", undefined);
		expect(decodeCloudBrowserFrame(frame).sequence).toBe(9n);
		const invalid = packet({ seq: 1, ts: 1 }); new Uint8Array(invalid)[4] = 0xff;
		expect(() => decodeCloudBrowserFrame(invalid)).toThrow();
	});

	it("aggregates separate tabs, active, URL, loading, history and viewport messages", () => {
		let state = initialCloudBrowserState();
		for (const message of [
			{ t: "tabs", tabs: [{ id: "tab-1", title: "Page", url: "https://example.com/" }] },
			{ t: "active", id: "tab-1" }, { t: "url", url: "https://example.com/next" },
			{ t: "loading", on: true }, { t: "history", back: true, fwd: false }, { t: "viewport", w: 900, h: 600, mine: false },
		]) state = projectCloudBrowserMessage(state, JSON.stringify(message)).state;
		expect(state).toMatchObject({ activeTabId: "tab-1", title: "Page", url: "https://example.com/next", loading: true, canGoBack: true, canGoForward: false, width: 900, height: 600, tabs: [{ id: "tab-1", active: true }] });
		expect(projectCloudBrowserMessage(state, '{"t":"dialog"}').notice).toMatch(/desktop/i);
		expect(projectCloudBrowserMessage(state, '{"t":"ui","data":{"kind":"clipboard","text":"private page text"}}').notice).toMatch(/desktop/i);
		expect(projectCloudBrowserMessage(state, '{"t":"control","owner":"agent"}').notice).toMatch(/agent/i);
		expect(projectCloudBrowserMessage(state, '{"t":"url","url":"https://user:password@example.com/"}').state.url).toBe("");
	});

	it("maps normalized pointers to CSS pixels and reverses Electron wheel signs", () => {
		const size = { width: 1000, height: 600 };
		expect(cloudBrowserCommand({ type: "input", payload: { kind: "pointer", phase: "down", x: 0.25, y: 0.5, button: "left" } }, size, 4)).toEqual({ t: "mouse", type: "mousePressed", seq: 4, x: 250, y: 300, button: "left", buttons: 1, clickCount: 1, mods: 0 });
		expect(cloudBrowserCommand({ type: "input", payload: { kind: "wheel", deltaX: 12, deltaY: -30 } }, size, 5)).toEqual({ t: "mouse", type: "mouseWheel", seq: 5, x: 500, y: 300, dx: -12, dy: 30, mods: 0 });
		for (const payload of [{ kind: "pointer", phase: "down", x: NaN, y: 0 }, { kind: "pointer", phase: "down", x: 2, y: 0 }, { kind: "wheel", deltaX: Infinity, deltaY: 0 }, { kind: "key", key: "Enter" }]) expect(cloudBrowserCommand({ type: "input", payload }, size, 6)).toBeNull();
	});

	it("only emits supported navigation and tab commands with safe targets", () => {
		const size = { width: 1000, height: 600 };
		expect(cloudBrowserCommand({ type: "navigate", payload: { action: "open", url: "https://example.com" } }, size, 1)).toEqual({ t: "nav", url: "https://example.com/" });
		expect(cloudBrowserCommand({ type: "navigate", payload: { action: "back" } }, size, 1)).toEqual({ t: "back" });
		expect(cloudBrowserCommand({ type: "tab", payload: { action: "new", url: "https://example.com" } }, size, 1)).toEqual({ t: "newtab" });
		expect(cloudBrowserCommand({ type: "tab", payload: { action: "select", tabId: "tab-1" } }, size, 1)).toEqual({ t: "tab", id: "tab-1" });
		for (const url of ["javascript:alert(1)", "file:///tmp/x", "https://user:secret@example.com", "https://", "about:blank"]) expect(cloudBrowserCommand({ type: "navigate", payload: { action: "open", url } }, size, 1)).toBeNull();
		expect(cloudBrowserCommand({ type: "tab", payload: { action: "close", tabId: "../x" } }, size, 1)).toBeNull();
	});
});
