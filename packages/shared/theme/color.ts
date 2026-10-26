/**
 * Minimal sRGB/OKLab color math for the theme catalog. Derived theme tokens are
 * resolved to concrete hex here (not CSS `color-mix`) because xterm reads
 * custom properties as raw strings and cannot resolve `color-mix()` itself.
 */

type Rgb = [number, number, number];
type Lab = [number, number, number];

function parseHex(hex: string): Rgb {
	const value = hex.replace("#", "");
	const full = value.length === 3 ? [...value].map((c) => c + c).join("") : value.slice(0, 6);
	return [0, 2, 4].map((i) => Number.parseInt(full.slice(i, i + 2), 16) / 255) as Rgb;
}

function toHex([r, g, b]: Rgb): string {
	return `#${[r, g, b]
		.map((c) => Math.round(Math.min(1, Math.max(0, c)) * 255).toString(16).padStart(2, "0"))
		.join("")}`;
}

const toLinear = (c: number) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
const fromLinear = (c: number) => (c <= 0.0031308 ? c * 12.92 : 1.055 * c ** (1 / 2.4) - 0.055);

function rgbToOklab(rgb: Rgb): Lab {
	const [r, g, b] = rgb.map(toLinear);
	const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
	const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
	const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
	return [
		0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
		1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
		0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
	];
}

function oklabToRgb([L, a, b]: Lab): Rgb {
	const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
	const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
	const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
	return [
		4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
		-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
		-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
	].map(fromLinear) as Rgb;
}

/** Mix `from` toward `to` in OKLab; `amount` is the share of `to` (0–1). */
export function mixColor(from: string, to: string, amount: number): string {
	const a = rgbToOklab(parseHex(from));
	const b = rgbToOklab(parseHex(to));
	return toHex(oklabToRgb([0, 1, 2].map((i) => a[i] + (b[i] - a[i]) * amount) as Lab));
}

/** Append an alpha channel to a hex color as #rrggbbaa. */
export function withAlpha(hex: string, alpha: number): string {
	return `${toHex(parseHex(hex))}${Math.round(alpha * 255).toString(16).padStart(2, "0")}`;
}

/** OKLab lightness (0–1); used to tell dark plates from light ones. */
export function lightness(hex: string): number {
	return rgbToOklab(parseHex(hex))[0];
}

function relativeLuminance(hex: string): number {
	const [r, g, b] = parseHex(hex).map(toLinear);
	return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** WCAG 2 contrast ratio between two opaque colors. */
export function contrastRatio(a: string, b: string): number {
	const [hi, lo] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
	return (hi + 0.05) / (lo + 0.05);
}

/** Perceptual distance between two colors: Euclidean in OKLab (0 = identical). */
export function colorDistance(a: string, b: string): number {
	const x = rgbToOklab(parseHex(a));
	const y = rgbToOklab(parseHex(b));
	return Math.hypot(x[0] - y[0], x[1] - y[1], x[2] - y[2]);
}
