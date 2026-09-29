import { describe, expect, it } from "vitest";
import { bridgeResult, browserCommandScript, parseBrowserBridgeMessage, parseBrowserContentAppearance } from "./mobileBrowserBridge";

describe("mobile browser bridge protocol", () => {
	it("embeds commands as JSON rather than executable text", () => {
		const script = browserCommandScript({
			type: "command",
			requestId: "r1",
			sessionId: "s1",
			action: "fill",
			args: { ref: "e1", value: "'); alert(1); ('" },
		});
		expect(script).toContain("window.__aoMobileBrowserBridge.run(");
		expect(script).toContain(JSON.stringify("'); alert(1); ('"));
	});

	it("returns structured ref metadata for one-command act matching", () => {
		expect(browserCommandScript({ type: "command", requestId: "r1", sessionId: "s1", action: "snapshot" }))
			.toContain("refs: refInfo");
	});

	it("accepts only tagged correlated results", () => {
		expect(parseBrowserBridgeMessage("{}")).toBeUndefined();
		const message = parseBrowserBridgeMessage(JSON.stringify({
			__aoMobileBrowser: true,
			requestId: "r1",
			ok: true,
			result: { text: "Save" },
		}));
		expect(message?.requestId).toBe("r1");
		expect(message && bridgeResult(message)).toEqual({ ok: true, result: { text: "Save" } });
	});

	it("preserves structured command failures", () => {
		const message = parseBrowserBridgeMessage(JSON.stringify({
			__aoMobileBrowser: true,
			requestId: "r2",
			ok: false,
			error: { code: "STALE_REFERENCE", message: "snapshot again" },
		}));
		expect(message && bridgeResult(message)).toEqual({
			ok: false,
			error: { code: "STALE_REFERENCE", message: "snapshot again" },
		});
	});

	it("accepts only valid page appearance reports", () => {
		expect(parseBrowserContentAppearance(JSON.stringify({ __aoMobileBrowserAppearance: "light" }))).toBe("light");
		expect(parseBrowserContentAppearance(JSON.stringify({ __aoMobileBrowserAppearance: "dark" }))).toBe("dark");
		expect(parseBrowserContentAppearance(JSON.stringify({ __aoMobileBrowserAppearance: "sepia" }))).toBeUndefined();
	});
});
