#!/usr/bin/env node
// Writes every harness mark as an iOS asset-catalog image set for SwiftUI menus.
//
// A SwiftUI menu draws an image at its own point size: an open submenu's header
// ignores any frame given to it, and `Image(uiImage:)` loads a bundled PNG at
// scale 1, so a 128px mark drew 128pt wide there. An asset-catalog image carries
// its scale, so these are 20pt marks at @1x, @2x and @3x, drawn in their own
// colours ("original" rendering) rather than tinted like a symbol. The marks
// that vanish on one background get their chip baked in, with the same inset
// and corner as `AgentLogo`, since a menu cannot draw one behind them.
//
// Run from the repo with the mobile app installed:
//   node packages/mobile/scripts/generate-harness-menu-icons.mjs
// It writes packages/mobile/assets/icons/harness/HarnessLogo-<key>.imageset, which
// plugins/withHarnessMenuIcons.js copies into the app's asset catalog.

import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { HARNESS_CHIP, LOGO_KEYS, chipColorFor } from "../lib/harnessLogo.ts";

const require = createRequire(import.meta.url);
const { PNG } = require("pngjs");

const here = dirname(fileURLToPath(import.meta.url));
const mobile = join(here, "..");
const output = join(mobile, "assets", "icons", "harness");
/** The mark's size in points, as `HarnessImage` draws it in the spawn rail. */
const POINTS = 20;
const SCALES = [1, 2, 3];

const hex = (value) => [1, 3, 5].map((index) => parseInt(value.slice(index, index + 2), 16));

/** Area-averaged downscale, in premultiplied alpha so edges keep their colour. */
function downscale(source, size) {
	const out = new Float64Array(size * size * 4);
	const ratio = source.width / size;
	for (let y = 0; y < size; y++) {
		for (let x = 0; x < size; x++) {
			let r = 0, g = 0, b = 0, a = 0, area = 0;
			const x0 = x * ratio, x1 = (x + 1) * ratio, y0 = y * ratio, y1 = (y + 1) * ratio;
			for (let sy = Math.floor(y0); sy < Math.ceil(y1); sy++) {
				const wy = Math.min(y1, sy + 1) - Math.max(y0, sy);
				for (let sx = Math.floor(x0); sx < Math.ceil(x1); sx++) {
					const w = (Math.min(x1, sx + 1) - Math.max(x0, sx)) * wy;
					const i = (sy * source.width + sx) * 4;
					const alpha = source.data[i + 3] / 255;
					r += source.data[i] * alpha * w; g += source.data[i + 1] * alpha * w; b += source.data[i + 2] * alpha * w; a += alpha * w;
					area += w;
				}
			}
			const o = (y * size + x) * 4;
			out[o] = r / area; out[o + 1] = g / area; out[o + 2] = b / area; out[o + 3] = a / area;
		}
	}
	return out;
}

/** How much of a pixel a rounded square covers, sampled 4×4. */
function roundedCoverage(x, y, size, radius) {
	let inside = 0;
	for (let sy = 0; sy < 4; sy++) {
		for (let sx = 0; sx < 4; sx++) {
			const px = x + (sx + 0.5) / 4, py = y + (sy + 0.5) / 4;
			const dx = Math.max(radius - px, 0, px - (size - radius));
			const dy = Math.max(radius - py, 0, py - (size - radius));
			if (dx * dx + dy * dy <= radius * radius) inside++;
		}
	}
	return inside / 16;
}

function render(source, size, chip) {
	const png = new PNG({ width: size, height: size });
	// Premultiplied canvas: the chip first, then the mark over it.
	const canvas = new Float64Array(size * size * 4);
	const inset = chip ? Math.round(size * 0.16) : 0;
	if (chip) {
		const [r, g, b] = hex(chip);
		const radius = Math.round(size * 0.28);
		for (let y = 0; y < size; y++) {
			for (let x = 0; x < size; x++) {
				const a = roundedCoverage(x, y, size, radius);
				const o = (y * size + x) * 4;
				canvas[o] = r * a; canvas[o + 1] = g * a; canvas[o + 2] = b * a; canvas[o + 3] = a;
			}
		}
	}
	const markSize = size - inset * 2;
	const mark = downscale(source, markSize);
	for (let y = 0; y < markSize; y++) {
		for (let x = 0; x < markSize; x++) {
			const m = (y * markSize + x) * 4;
			const o = ((y + inset) * size + (x + inset)) * 4;
			const keep = 1 - mark[m + 3];
			for (let c = 0; c < 4; c++) canvas[o + c] = mark[m + c] + canvas[o + c] * keep;
		}
	}
	for (let i = 0; i < size * size; i++) {
		const a = canvas[i * 4 + 3];
		png.data[i * 4 + 3] = Math.round(a * 255);
		for (let c = 0; c < 3; c++) png.data[i * 4 + c] = a > 0 ? Math.round(Math.min(255, canvas[i * 4 + c] / a)) : 0;
	}
	return PNG.sync.write(png);
}

rmSync(output, { recursive: true, force: true });
for (const key of [...LOGO_KEYS].sort()) {
	const source = PNG.sync.read(readFileSync(join(mobile, "assets", "agents", `${key}.png`)));
	const chip = chipColorFor(key);
	const set = join(output, `HarnessLogo-${key}.imageset`);
	mkdirSync(set, { recursive: true });
	const images = SCALES.map((scale) => {
		const filename = `${key}@${scale}x.png`;
		writeFileSync(join(set, filename), render(source, POINTS * scale, chip));
		return { filename, idiom: "universal", scale: `${scale}x` };
	});
	writeFileSync(join(set, "Contents.json"), `${JSON.stringify({ images, info: { author: "xcode", version: 1 }, properties: { "template-rendering-intent": "original" } }, null, 2)}\n`);
}
console.log(`Wrote ${LOGO_KEYS.size} harness image sets (chips: dark ${HARNESS_CHIP.dark}, light ${HARNESS_CHIP.light}).`);
