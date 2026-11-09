import { describe, expect, it } from "vitest";
import { classifyFaviconPixels } from "./favicon-tone";

type Pixel = [number, number, number, number];
const CLEAR: Pixel = [0, 0, 0, 0];

function image(ink: Pixel, coverage: number): Uint8ClampedArray {
	const total = 16 * 16;
	const pixels = new Uint8ClampedArray(total * 4);
	for (let index = 0; index < total; index += 1) pixels.set(index < total * coverage ? ink : CLEAR, index * 4);
	return pixels;
}

describe("classifyFaviconPixels", () => {
	it.each<[string, Pixel, number, string]>([
		["a near-black glyph on transparency", [36, 41, 46, 255], 0.5, "dark"],
		["a near-white glyph on transparency", [245, 245, 245, 255], 0.5, "light"],
		["a colourful glyph", [230, 40, 40, 255], 0.5, "fixed"],
		["a dark icon with its own background", [36, 41, 46, 255], 1, "fixed"],
		["a mid-grey glyph that reads on either theme", [128, 128, 128, 255], 0.5, "fixed"],
		["a fully transparent image", CLEAR, 1, "fixed"],
	])("treats %s as %s", (_name, ink, coverage, tone) => {
		expect(classifyFaviconPixels(image(ink, coverage))).toBe(tone);
	});

	it("ignores nearly transparent antialiasing pixels", () => {
		const pixels = image([36, 41, 46, 255], 0.5);
		pixels.set([255, 255, 255, 40], pixels.length - 4);
		expect(classifyFaviconPixels(pixels)).toBe("dark");
	});
});
