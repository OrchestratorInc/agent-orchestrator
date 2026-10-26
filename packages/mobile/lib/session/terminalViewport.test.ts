import { describe, expect, it, vi } from "vitest";

import { adjustTerminalViewport, fitTerminalViewport } from "./terminalViewport";

describe("terminal viewport zoom", () => {
	it("adjusts the live WebView viewport without replacing the terminal renderer", () => {
		const injectJavaScript = vi.fn();
		const renderer = { injectJavaScript };

		expect(adjustTerminalViewport(renderer, 1)).toBe(true);
		expect(adjustTerminalViewport(renderer, -1)).toBe(true);
		expect(injectJavaScript.mock.calls).toEqual([
			["window.__aoAdjustTerminalZoom(1); true;"],
			["window.__aoAdjustTerminalZoom(-1); true;"],
		]);
	});

	it("fits the grid's width through the same injection", () => {
		const injectJavaScript = vi.fn();

		expect(fitTerminalViewport({ injectJavaScript })).toBe(true);
		expect(injectJavaScript).toHaveBeenCalledWith("window.__aoFitTerminalWidth(); true;");
	});

	it("does nothing until the terminal renderer is ready", () => {
		expect(adjustTerminalViewport(null, 1)).toBe(false);
		expect(fitTerminalViewport(null)).toBe(false);
	});
});
