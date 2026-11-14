import { describe, expect, it } from "vitest";
import { clampZoom, MAX_FRAME_ZOOM, pinchZoom, unzoomed, unzoomPoint } from "./frameZoom";

const size = { width: 400, height: 600 };

function expectPoint(actual: { x: number; y: number }, expected: { x: number; y: number }) {
	expect(actual.x).toBeCloseTo(expected.x);
	expect(actual.y).toBeCloseTo(expected.y);
}

describe("live browser zoom", () => {
	it("leaves points alone at the desktop's own size", () => {
		expectPoint(unzoomPoint({ x: 30, y: 500 }, unzoomed, size), { x: 30, y: 500 });
	});

	it("maps a tap on a zoomed frame back to the spot drawn under the finger", () => {
		// Doubled about the centre and moved left: the centre now shows (250, 300).
		const zoom = { scale: 2, x: -100, y: 0 };
		expectPoint(unzoomPoint({ x: 200, y: 300 }, zoom, size), { x: 250, y: 300 });
		expectPoint(unzoomPoint({ x: 0, y: 0 }, zoom, size), { x: 150, y: 150 });
	});

	it("keeps the spot under the fingers there while they spread", () => {
		const focal = { x: 100, y: 120 };
		const zoom = pinchZoom(unzoomed, focal, focal, 2, size);
		expect(zoom.scale).toBe(2);
		expectPoint(unzoomPoint(focal, zoom, size), focal);
	});

	it("pans with the fingers as they move together", () => {
		const start = { scale: 2, x: 0, y: 0 };
		const zoom = pinchZoom(start, { x: 200, y: 300 }, { x: 240, y: 280 }, 1, size);
		expectPoint(zoom, { x: 40, y: -20 });
		expectPoint(unzoomPoint({ x: 240, y: 280 }, zoom, size), unzoomPoint({ x: 200, y: 300 }, start, size));
	});

	it("never shrinks the frame below its fitted size or past the readable limit", () => {
		expect(pinchZoom(unzoomed, { x: 10, y: 10 }, { x: 10, y: 10 }, 0.3, size)).toMatchObject({ scale: 1 });
		expect(pinchZoom({ scale: 3, x: 0, y: 0 }, { x: 0, y: 0 }, { x: 0, y: 0 }, 5, size).scale).toBe(MAX_FRAME_ZOOM);
	});

	it("stops a zoomed frame at the screen's edges", () => {
		expect(clampZoom({ scale: 2, x: 900, y: -900 }, size)).toEqual({ scale: 2, x: 200, y: -300 });
		const flat = clampZoom({ scale: 1, x: 50, y: 50 }, size);
		expect(Math.abs(flat.x)).toBe(0);
		expect(Math.abs(flat.y)).toBe(0);
	});
});
