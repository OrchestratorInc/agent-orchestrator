import { describe, expect, it } from "vitest";
import { backdropOpacity, boundTranslation, maxTranslation, shouldDismiss, zoomTranslation } from "./imageZoom";

describe("image zoom", () => {
	// A point drawn at `focal` sits at (focal - translate) / scale in the image;
	// after the zoom it has to be drawn at `focal` again.
	it("keeps the point under the fingers still while it scales", () => {
		const scale = 1.5;
		const translate = 40;
		const focal = 120;
		const factor = 2;
		const point = (focal - translate) / scale;
		const next = zoomTranslation(translate, focal, factor);
		expect(next + point * scale * factor).toBeCloseTo(focal);
	});

	it("lets the zoomed image slide only until an edge would come away", () => {
		expect(maxTranslation(400, 1)).toBe(0);
		expect(maxTranslation(400, 2)).toBe(200);
		expect(boundTranslation(350, 400, 2)).toBe(200);
		expect(boundTranslation(-350, 400, 2)).toBe(-200);
		expect(boundTranslation(50, 400, 1)).toBe(0);
	});

	it("closes on a long enough drag or a flick, either way", () => {
		expect(shouldDismiss(60, 200)).toBe(false);
		expect(shouldDismiss(160, 0)).toBe(true);
		expect(shouldDismiss(-160, 0)).toBe(true);
		expect(shouldDismiss(20, 1500)).toBe(true);
	});

	it("thins the backdrop as the image is dragged away, never to nothing", () => {
		expect(backdropOpacity(0, 800)).toBe(1);
		expect(backdropOpacity(240, 800)).toBeCloseTo(0.5);
		expect(backdropOpacity(-2000, 800)).toBe(0.2);
	});
});
