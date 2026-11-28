import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PANEL_BORDER_PX, RESIZE_LINE_INSET_PX, RESIZE_LINE_WIDTH_PX, ResizeHandle, resizeLineLeft, resizeLineSpan } from "./ResizeHandle";

const rect = (left: number, width: number) => ({ left, right: left + width, width, top: 0, bottom: 800, height: 800, x: left, y: 0, toJSON: () => ({}) }) as DOMRect;

/** The line's page position: the strip's left plus the line's offset inside it. */
function lineOnPage(handleLeft: number, handleWidth: number, edge: DOMRect) {
	const panel = document.createElement("div");
	panel.getBoundingClientRect = () => edge;
	render(<ResizeHandle edge={() => panel} side="right" />);
	const handle = screen.getByTestId("resize-handle");
	handle.getBoundingClientRect = () => rect(handleLeft, handleWidth);
	fireEvent.pointerEnter(handle);
	const line = handle.querySelector<HTMLElement>("[data-resize-line]")!;
	const left = handleLeft + Number.parseFloat(line.style.left);
	return { left, center: left + RESIZE_LINE_WIDTH_PX / 2, line };
}

describe("ResizeHandle hover line", () => {
	// Measured in the app (Interface v2): the hit strip is 12px wide and
	// centered on the boundary, while the panel's 1px border sits elsewhere.
	it("centers on the center panel's left border, not the strip's center (sidebar)", () => {
		const { center } = lineOnPage(236, 12, rect(243, 1069));
		expect(center).toBe(243 + PANEL_BORDER_PX / 2);
	});

	it("centers on the inspector's left border (inspector)", () => {
		const { center } = lineOnPage(806, 12, rect(811, 500));
		expect(center).toBe(811 + PANEL_BORDER_PX / 2);
	});

	// Under the floating top bar the rail's border is transparent and the
	// session column draws the line on its last pixel (810 when it ends at 811).
	it("centers on the session column's last pixel under the floating top bar (inspector)", () => {
		const { center } = lineOnPage(806, 12, rect(244, 567));
		expect(center).toBe(810 + PANEL_BORDER_PX / 2);
	});

	it("takes the right border when the strip is nearer that side", () => {
		const left = resizeLineLeft(rect(1306, 12), rect(300, 1012));
		expect(1306 + left + RESIZE_LINE_WIDTH_PX / 2).toBe(1311 + PANEL_BORDER_PX / 2);
	});

	it("spans the panel's height, 20px short of its top and bottom", () => {
		const panel = document.createElement("div");
		panel.getBoundingClientRect = () => ({ ...rect(243, 1069), top: 50, bottom: 760 }) as DOMRect;
		render(<ResizeHandle edge={() => panel} side="right" />);
		const handle = screen.getByTestId("resize-handle");
		handle.getBoundingClientRect = () => rect(236, 12);
		fireEvent.pointerEnter(handle);
		const line = handle.querySelector<HTMLElement>("[data-resize-line]")!;
		expect(line.style.top).toBe(`${50 + RESIZE_LINE_INSET_PX}px`);
		expect(line.style.bottom).toBe(`${800 - 760 + RESIZE_LINE_INSET_PX}px`);
		expect(resizeLineSpan({ top: 0, bottom: 800 }, { top: -10, bottom: 900 })).toEqual({ top: RESIZE_LINE_INSET_PX, bottom: RESIZE_LINE_INSET_PX });
	});

	// As wide as the hairline, it would vanish into it on hover.
	it("is wider than the hairline it covers, and nothing else shifts it", () => {
		const { line } = lineOnPage(236, 12, rect(243, 1069));
		expect(RESIZE_LINE_WIDTH_PX).toBeGreaterThan(PANEL_BORDER_PX);
		expect(line).toHaveClass("w-0.5");
		expect(line.className).not.toMatch(/translate/);
	});
});

/**
 * Without an edge the line falls back to the strip's center, which is where it
 * drifted off the border before. Every resize handle in the app names the
 * panel it resizes, so a new one cannot quietly bring the drift back.
 */
describe("every ResizeHandle names its edge", () => {
	const root = path.resolve(process.cwd(), "src/renderer");
	const files = (directory: string): string[] =>
		readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
			const absolute = path.join(directory, entry.name);
			if (entry.isDirectory()) return files(absolute);
			return /\.tsx$/.test(entry.name) && !/\.test\.tsx$/.test(entry.name) ? [absolute] : [];
		});

	it("passes edge= on each <ResizeHandle>", () => {
		const missing: string[] = [];
		for (const file of files(root)) {
			const source = readFileSync(file, "utf8");
			for (const match of source.matchAll(/<ResizeHandle\b[^>]*?\/?>/gs)) {
				if (!/\bedge=/.test(match[0])) missing.push(`${path.relative(root, file)}: ${match[0].slice(0, 80)}`);
			}
		}
		expect(missing).toEqual([]);
	});
});

/**
 * The line is placed from the edge it measures; a stylesheet offset on top of
 * that (the old `translateX(-1px)` nudge) would move it off the edge again.
 */
describe("no stylesheet rule moves the resize line", () => {
	it("leaves [data-resize-line] unpositioned in styles.css", () => {
		const css = readFileSync(path.resolve(process.cwd(), "src/renderer/styles.css"), "utf8");
		const rules = [...css.matchAll(/([^{}]*\[data-resize-line\][^{}]*)\{([^}]*)\}/g)];
		const moving = rules.filter(([, , body]) => /\b(transform|translate|left|right|margin|inset)\s*:/.test(body)).map(([, selector]) => selector.trim());
		expect(moving).toEqual([]);
	});
});

/**
 * The line straddles the divider, so half of it sits just outside the panel
 * that holds the handle. A panel clipped flush to its own box cuts that half
 * off and the line all but vanishes, which is how it went missing before.
 */
describe("the panels holding a resize handle leave room for its line", () => {
	const read = (file: string) => readFileSync(path.resolve(process.cwd(), "src/renderer", file), "utf8");

	it("lets the open sidebar paint past its right edge", () => {
		const rule = read("styles.css").match(/html\[data-new-desktop-ui\] \.sidebar-rail-clip \{([^}]*)\}/)?.[1] ?? "";
		const right = rule.match(/clip-path:\s*inset\(\s*\S+\s+(\S+)/)?.[1] ?? "";
		expect(Number.parseFloat(right)).toBeLessThanOrEqual(-RESIZE_LINE_WIDTH_PX);
	});

	it("lets the inspector paint past its left edge", () => {
		const container = read("components/SessionInspectorRail.tsx").match(/className="([^"]*)"[^>]*data-slot="inspector-container"/s)?.[1] ?? "";
		expect(container).not.toMatch(/\boverflow-hidden\b/);
		expect(container).toContain(`[clip-path:inset(0_0_0_-${RESIZE_LINE_WIDTH_PX}px)]`);
	});
});
