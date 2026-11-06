import { act, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ArtifactZoomController } from "../lib/artifact-zoom";
import { ZoomableImage } from "./ZoomableImage";

function size(element: HTMLElement, values: Record<string, number>) {
	for (const [name, value] of Object.entries(values)) Object.defineProperty(element, name, { configurable: true, value });
}

describe("ZoomableImage", () => {
	it("pans a zoomed image only as far as its fitted size allows", () => {
		const controller = new ArtifactZoomController();
		render(<ZoomableImage alt="wide" controller={controller} src="wide.png" />);
		const image = screen.getByRole("img", { name: "wide" });
		const box = image.parentElement!;
		// A 10:1 image in a square pane: fitted to 100x10, so 300x30 at 300%.
		size(box, { clientHeight: 100, clientWidth: 100 });
		size(image, { naturalHeight: 100, naturalWidth: 1_000 });
		box.getBoundingClientRect = () => ({ bottom: 100, height: 100, left: 0, right: 100, top: 0, width: 100, x: 0, y: 0, toJSON: () => ({}) });
		act(() => controller.setZoom(3));

		box.dispatchEvent(new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaX: -500, deltaY: 500 }));

		// Horizontally it may move until its edge meets the pane's; vertically it still fits, so stays centered.
		expect(image.style.transform).toMatch(/^translate3d\(100px, 0px, 0(px)?\) scale\(3\)$/);
	});
});

describe("ZoomableImage remount", () => {
	it("paints a new image at the viewer's current zoom", () => {
		const controller = new ArtifactZoomController();
		controller.setZoom(3);
		// A rewritten file or a fresh cloud link mounts a new image under the same zoom.
		render(<ZoomableImage alt="chart" controller={controller} src="chart.png?v=2" />);
		expect(screen.getByRole("img", { name: "chart" }).style.transform).toMatch(/scale\(3\)$/);
	});
});
