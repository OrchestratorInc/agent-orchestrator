import { describe, expect, it } from "vitest";
import { clampSplitSize, edgeAt, paneBox, paneClientRect, reconcileSplit, type SplitLayout } from "./pane-split";

const rect = { left: 100, top: 50, width: 800, height: 600 };

describe("edgeAt", () => {
	it("picks the left or right half across most of the area", () => {
		expect(edgeAt(rect, 200, 350)).toBe("left");
		expect(edgeAt(rect, 800, 350)).toBe("right");
	});

	it("picks top or bottom only in a band near those edges", () => {
		expect(edgeAt(rect, 500, 60)).toBe("top");
		expect(edgeAt(rect, 500, 640)).toBe("bottom");
		// Near a corner the closer side edge wins.
		expect(edgeAt(rect, 110, 60)).toBe("left");
	});

	it("is null outside the area", () => {
		expect(edgeAt(rect, 50, 300)).toBeNull();
		expect(edgeAt(rect, 500, 20)).toBeNull();
	});
});

describe("paneClientRect", () => {
	it("places the side pane on its side at its fraction", () => {
		expect(paneClientRect(rect, "side", "right", 0.25)).toEqual({ left: 700, top: 50, width: 200, height: 600 });
		expect(paneClientRect(rect, "main", "right", 0.25)).toEqual({ left: 100, top: 50, width: 600, height: 600 });
		expect(paneClientRect(rect, "side", "top", 0.5)).toEqual({ left: 100, top: 50, width: 800, height: 300 });
		expect(paneClientRect(rect, "main", "top", 0.5)).toEqual({ left: 100, top: 350, width: 800, height: 300 });
	});
});

describe("paneBox", () => {
	it("fills the area when there is no split", () => {
		expect(paneBox("main", null)).toMatchObject({ left: "0px", top: "0px", width: "100%", height: "100%" });
	});

	it("reads the size variable so a resize needs no render", () => {
		const side = paneBox("side", "left");
		expect(side.width).toBe("calc(var(--pane-split-size) * 100%)");
		expect(paneBox("main", "left").left).toBe("calc(var(--pane-split-size) * 100%)");
	});

	it("trims the side pane's strip from the top", () => {
		expect(paneBox("side", "right", "36px")).toMatchObject({ top: "calc(0px + 36px)", height: "calc(100% - 36px)" });
	});
});

describe("reconcileSplit", () => {
	const layout: SplitLayout = { side: "right", size: 0.5, keys: ["a", "b", "c"], active: "b" };

	it("keeps an unchanged layout identical", () => {
		expect(reconcileSplit(layout, new Set(["a", "b", "c", "d"]))).toBe(layout);
	});

	it("hands a closed active tab to the one before it", () => {
		expect(reconcileSplit(layout, new Set(["a", "c"]))).toEqual({ ...layout, keys: ["a", "c"], active: "a" });
	});

	it("collapses when the last tab is gone", () => {
		expect(reconcileSplit(layout, new Set(["x"]))).toBeNull();
	});
});

describe("clampSplitSize", () => {
	it("keeps both panes usable", () => {
		expect(clampSplitSize(0.05)).toBe(0.2);
		expect(clampSplitSize(0.95)).toBe(0.8);
		expect(clampSplitSize(Number.NaN)).toBe(0.5);
	});
});
