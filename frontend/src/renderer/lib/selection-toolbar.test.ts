import { describe, expect, it, vi } from "vitest";
import {
	selectionToolbarPosition as place,
	selectionViewport,
} from "./selection-toolbar";

const viewport = new DOMRect(100, 100, 400, 300);
const size = { width: 160, height: 32 };

describe("selection toolbar geometry", () => {
	it("intersects nested clipping panes with a zoomed visual viewport", () => {
		const outer = document.createElement("div");
		const inner = document.createElement("div");
		outer.append(inner);
		document.body.append(outer);
		outer.style.overflowX = "hidden";
		outer.style.overflowY = "hidden";
		vi.spyOn(outer, "getBoundingClientRect").mockReturnValue(
			new DOMRect(100, 100, 300, 300),
		);
		vi.spyOn(inner, "getBoundingClientRect").mockReturnValue(
			new DOMRect(50, 150, 600, 200),
		);
		vi.stubGlobal("visualViewport", {
			offsetLeft: 20,
			offsetTop: 30,
			width: 200,
			height: 200,
		});
		try {
			expect(selectionViewport(inner)).toEqual({
				left: 100,
				right: 220,
				top: 150,
				bottom: 230,
			});
		} finally {
			vi.unstubAllGlobals();
			outer.remove();
		}
	});
	it("anchors tall selections to their first line, flips below near the top, and pins after it leaves", () => {
		const extent = new DOMRect(120, 200, 200, 1200);
		expect(
			place(new DOMRect(120, 200, 200, 20), viewport, size, extent).top,
		).toBe(162);
		expect(
			place(new DOMRect(120, 120, 200, 20), viewport, size, extent).top,
		).toBe(146);
		expect(
			place(new DOMRect(120, 50, 200, 20), viewport, size, extent),
		).toMatchObject({ top: 108, visible: true });
	});
	it("keeps a viewport-spanning range at the pane top", () => {
		expect(place(new DOMRect(120, -400, 200, 1200), viewport, size)).toEqual({
			left: 140,
			top: 108,
			visible: true,
		});
	});
	it.each([-1000, 401])("hides an entirely offscreen range at %s", (top) => {
		expect(place(new DOMRect(120, top, 200, 20), viewport, size).visible).toBe(
			false,
		);
	});
	it("keeps a partially visible range actionable even when its anchor is above the pane", () => {
		const position = place(new DOMRect(120, 50, 200, 100), viewport, size);
		expect(position.visible).toBe(true);
		expect(position.top).toBeGreaterThanOrEqual(108);
		expect(position.top + size.height).toBeLessThanOrEqual(392);
	});
	it.each([new DOMRect(-50, 120, 600, 20), new DOMRect(450, 380, 40, 15)])(
		"clamps the complete toolbar on every edge",
		(range) => {
			const position = place(range, viewport, size);
			expect(position.left).toBeGreaterThanOrEqual(108);
			expect(position.left + size.width).toBeLessThanOrEqual(492);
			expect(position.top).toBeGreaterThanOrEqual(108);
			expect(position.top + size.height).toBeLessThanOrEqual(392);
		},
	);
	it("uses the measured wrapped height on a narrow pane", () => {
		expect(
			place(new DOMRect(0, -100, 100, 900), new DOMRect(0, 50, 100, 300), {
				width: 84,
				height: 70,
			}),
		).toMatchObject({ left: 8, top: 58, visible: true });
	});
	it("rejects an empty clipped viewport", () => {
		expect(
			place(new DOMRect(0, 0, 200, 200), new DOMRect(0, 0, 0, 0), size).visible,
		).toBe(false);
	});
});
