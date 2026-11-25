/**
 * Literal CSS colors to hex. Theme files (theme-file.ts) may write a color as
 * hex, rgb(), hsl(), oklch() or oklab(); the app's color math and xterm both
 * need hex, so every theme color is resolved here first. Colors outside sRGB
 * lose chroma until they fit, as browsers do for oklch().
 */

type Triple = [number, number, number];

const NAMED: Record<string, string> = { black: "#000000", white: "#ffffff", transparent: "#00000000" };

function clamp01(value: number): number {
	return Math.min(1, Math.max(0, value));
}

function channelHex(value: number): string {
	return Math.round(clamp01(value) * 255)
		.toString(16)
		.padStart(2, "0");
}

function toHex([r, g, b]: Triple, alpha: number): string {
	const hex = `#${channelHex(r)}${channelHex(g)}${channelHex(b)}`;
	return alpha >= 1 ? hex : `${hex}${channelHex(alpha)}`;
}

/** A number or percentage; `scale` is what 100% means. */
function numberOrPercent(token: string, scale: number): number | null {
	if (token === "none") return 0;
	const percent = token.endsWith("%");
	const value = Number(percent ? token.slice(0, -1) : token);
	if (!Number.isFinite(value) || token === "" || token === "%") return null;
	return percent ? (value / 100) * scale : value;
}

function hueDegrees(token: string): number | null {
	if (token === "none") return 0;
	const match = /^(-?[\d.]+(?:e-?\d+)?)(deg|rad|grad|turn)?$/.exec(token);
	if (!match) return null;
	const value = Number(match[1]);
	const unit = match[2] ?? "deg";
	const degrees = unit === "rad" ? (value * 180) / Math.PI : unit === "grad" ? value * 0.9 : unit === "turn" ? value * 360 : value;
	// 1e308turn is a finite number of turns and an infinite number of degrees.
	return Number.isFinite(degrees) ? degrees : null;
}

const fromLinear = (c: number) => (c <= 0.0031308 ? c * 12.92 : 1.055 * c ** (1 / 2.4) - 0.055);

function oklabToLinearRgb(L: number, a: number, b: number): Triple {
	const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
	const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
	const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
	return [
		4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
		-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
		-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
	];
}

function inGamut(rgb: Triple): boolean {
	return rgb.every((c) => c >= -0.0005 && c <= 1.0005);
}

/** OKLCH to sRGB, dropping chroma (not lightness or hue) until the color fits. */
function oklchToRgb(L: number, C: number, h: number): Triple {
	const lightness = clamp01(L);
	const radians = (h * Math.PI) / 180;
	const at = (chroma: number) => oklabToLinearRgb(lightness, chroma * Math.cos(radians), chroma * Math.sin(radians));
	let rgb = at(C);
	if (!inGamut(rgb)) {
		let low = 0;
		let high = C;
		for (let step = 0; step < 20; step++) {
			const middle = (low + high) / 2;
			if (inGamut(at(middle))) low = middle;
			else high = middle;
		}
		rgb = at(low);
	}
	return rgb.map((c) => fromLinear(clamp01(c))) as Triple;
}

function hslToRgb(h: number, s: number, l: number): Triple {
	const hue = ((h % 360) + 360) % 360;
	const saturation = clamp01(s);
	const lightness = clamp01(l);
	const chroma = (1 - Math.abs(2 * lightness - 1)) * saturation;
	const x = chroma * (1 - Math.abs(((hue / 60) % 2) - 1));
	const m = lightness - chroma / 2;
	const sector = Math.floor(hue / 60);
	const [r, g, b] = [
		[chroma, x, 0],
		[x, chroma, 0],
		[0, chroma, x],
		[0, x, chroma],
		[x, 0, chroma],
		[chroma, 0, x],
	][sector] as Triple;
	return [r + m, g + m, b + m];
}

function parseHex(value: string): string | null {
	if (!/^#(?:[0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/.test(value)) return null;
	const digits = value.slice(1);
	const full = digits.length <= 4 ? [...digits].map((c) => c + c).join("") : digits;
	return full.length === 8 && full.endsWith("ff") ? `#${full.slice(0, 6)}` : `#${full}`;
}

/**
 * Resolves a literal CSS color to `#rrggbb`, or `#rrggbbaa` when it is not
 * opaque. Returns null for anything else: variables, `color-mix()`, keywords
 * other than black, white and transparent.
 */
export function cssColorToHex(input: unknown): string | null {
	if (typeof input !== "string") return null;
	try {
		const hex = parseCssColor(input.trim().toLowerCase());
		// A color the math could not finish (an overflow somewhere) is no color.
		return hex !== null && /^#[0-9a-f]{6}(?:[0-9a-f]{2})?$/.test(hex) ? hex : null;
	} catch {
		return null;
	}
}

function parseCssColor(value: string): string | null {
	if (value.startsWith("#")) return parseHex(value);
	if (Object.hasOwn(NAMED, value)) return NAMED[value];
	const call = /^(rgba?|hsla?|oklch|oklab)\(([^()]*)\)$/.exec(value);
	if (!call) return null;
	const [, fn, body] = call;
	const [channels, alphaPart, ...extra] = body.split("/");
	if (extra.length > 0) return null;
	const parts = channels
		.trim()
		.split(/[\s,]+/)
		.filter(Boolean);
	// Legacy comma syntax carries alpha as a fourth value.
	const alphaToken = alphaPart?.trim() ?? (parts.length === 4 ? parts.pop() : undefined);
	if (parts.length !== 3) return null;
	const alpha = alphaToken === undefined ? 1 : numberOrPercent(alphaToken, 1);
	if (alpha === null) return null;
	const [first, second, third] = parts;

	if (fn === "rgb" || fn === "rgba") {
		const rgb = [first, second, third].map((token) => numberOrPercent(token, 255));
		if (rgb.some((c) => c === null)) return null;
		return toHex(rgb.map((c) => (c as number) / 255) as Triple, clamp01(alpha));
	}
	if (fn === "hsl" || fn === "hsla") {
		const h = hueDegrees(first);
		const s = numberOrPercent(second, 1);
		const l = numberOrPercent(third, 1);
		if (h === null || s === null || l === null || !second.endsWith("%") || !third.endsWith("%")) return null;
		return toHex(hslToRgb(h, s, l), clamp01(alpha));
	}
	const L = numberOrPercent(first, 1);
	if (L === null) return null;
	if (fn === "oklch") {
		const C = numberOrPercent(second, 0.4);
		const h = hueDegrees(third);
		if (C === null || h === null) return null;
		return toHex(oklchToRgb(L, Math.max(0, C), h), clamp01(alpha));
	}
	const a = numberOrPercent(second, 0.4);
	const b = numberOrPercent(third, 0.4);
	if (a === null || b === null) return null;
	return toHex(oklchToRgb(L, Math.hypot(a, b), (Math.atan2(b, a) * 180) / Math.PI), clamp01(alpha));
}
