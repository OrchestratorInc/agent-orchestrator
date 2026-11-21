/**
 * How a favicon's pixels relate to the card behind it. Most favicons are drawn
 * for a light browser tab, and a one-colour glyph with no background (GitHub's
 * is near-black) vanishes on a dark card.
 *
 * - "dark" / "light": a one-colour glyph on transparency that can be inverted.
 * - "fixed": has its own colours or background, so it reads on any card.
 * - "unknown": the pixels cannot be read (the host sends no CORS headers).
 */
export type FaviconTone = "dark" | "light" | "fixed" | "unknown";

const OPAQUE_ALPHA = 128;
const TILE_COVERAGE = 0.9;
const MAX_MONOCHROME_CHROMA = 0.15;
const DARK_LUMA = 0.35;
const LIGHT_LUMA = 0.75;
const SAMPLE_SIZE = 16;

/** Classifies RGBA pixel data; pure so the thresholds can be tested directly. */
export function classifyFaviconPixels(pixels: Uint8ClampedArray): FaviconTone {
	const total = pixels.length / 4;
	let opaque = 0;
	let chroma = 0;
	let luma = 0;
	for (let offset = 0; offset < pixels.length; offset += 4) {
		if (pixels[offset + 3] < OPAQUE_ALPHA) continue;
		const r = pixels[offset] / 255;
		const g = pixels[offset + 1] / 255;
		const b = pixels[offset + 2] / 255;
		opaque += 1;
		chroma += Math.max(r, g, b) - Math.min(r, g, b);
		luma += 0.2126 * r + 0.7152 * g + 0.0722 * b;
	}
	if (opaque === 0 || opaque / total >= TILE_COVERAGE) return "fixed";
	if (chroma / opaque > MAX_MONOCHROME_CHROMA) return "fixed";
	const averageLuma = luma / opaque;
	if (averageLuma <= DARK_LUMA) return "dark";
	if (averageLuma >= LIGHT_LUMA) return "light";
	return "fixed";
}

/** Reads a loaded image's tone. The image must have been loaded with CORS. */
export function measureFaviconTone(image: HTMLImageElement): FaviconTone {
	try {
		const canvas = document.createElement("canvas");
		canvas.width = SAMPLE_SIZE;
		canvas.height = SAMPLE_SIZE;
		const context = canvas.getContext("2d", { willReadFrequently: true });
		if (!context) return "unknown";
		context.drawImage(image, 0, 0, SAMPLE_SIZE, SAMPLE_SIZE);
		return classifyFaviconPixels(context.getImageData(0, 0, SAMPLE_SIZE, SAMPLE_SIZE).data);
	} catch {
		return "unknown";
	}
}
