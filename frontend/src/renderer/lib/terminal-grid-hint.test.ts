import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Each import is a fresh module, standing in for an app launch.
async function launch() {
	vi.resetModules();
	return import("./terminal-grid-hint");
}

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe("terminal grid hint", () => {
	it("has no hint before any pane measured", async () => {
		const { paneTerminalGridHint } = await launch();
		expect(paneTerminalGridHint()).toBeNull();
	});

	it("keeps the last pane grid across launches", async () => {
		const first = await launch();
		first.rememberPaneTerminalGrid(146, 37);
		expect(first.paneTerminalGridHint()).toEqual({ cols: 146, rows: 37 });

		const second = await launch();
		expect(second.paneTerminalGridHint()).toEqual({ cols: 146, rows: 37 });
	});

	it("ignores out-of-range grids and corrupt saved values", async () => {
		const first = await launch();
		first.rememberPaneTerminalGrid(1, 37);
		first.rememberPaneTerminalGrid(146, 0);
		expect(first.paneTerminalGridHint()).toBeNull();

		localStorage.setItem("ao.terminalPaneGrid", "{not json");
		expect((await launch()).paneTerminalGridHint()).toBeNull();

		localStorage.setItem("ao.terminalPaneGrid", JSON.stringify({ cols: 5000, rows: 37 }));
		expect((await launch()).paneTerminalGridHint()).toBeNull();
	});

	it("still works for this launch when storage throws", async () => {
		vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
			throw new Error("blocked");
		});
		vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
			throw new Error("blocked");
		});
		const { paneTerminalGridHint, rememberPaneTerminalGrid } = await launch();
		expect(paneTerminalGridHint()).toBeNull();
		rememberPaneTerminalGrid(120, 30);
		expect(paneTerminalGridHint()).toEqual({ cols: 120, rows: 30 });
	});
});
