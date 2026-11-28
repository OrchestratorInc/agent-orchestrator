#!/usr/bin/env node
// Copies every harness brand mark from the desktop into the app.
//
// The desktop is the source of truth: frontend/src/renderer/components/AgentAvatar.tsx
// maps each harness to an asset, or to a { light, dark } pair where the brand ships
// separate marks for each background. React Native cannot draw SVG as an image, so
// each asset is rasterised here, and the pairs stay pairs: the app draws the one
// that matches its colour scheme, exactly as the desktop does.
//
// Run it after any change to the desktop's marks:
//   node packages/mobile/scripts/sync-harness-logos.mjs
// lib/harnessLogo.test.ts fails until it has been run.
//
// It writes:
//   assets/agents/<desktop file name>.png   one 128px PNG per desktop asset
//   lib/harnessLogoManifest.ts               which harness uses which files (plain data)
//   lib/harnessLogoAssets.ts                 the matching `require`s for Metro
//   assets/icons/harness/HarnessLogo-<key>.imageset
//       20pt marks at @1x/@2x/@3x for SwiftUI menus, with the dark mark as the
//       catalog's dark appearance. plugins/withHarnessMenuIcons.js copies these into
//       the iOS asset catalog on prebuild, so they reach users with a store build.

import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, extname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { DESKTOP_ASSETS, readDesktopLogos, sourceHash } from "./desktop-harness-logos.mjs";

const require = createRequire(import.meta.url);
const { PNG } = require("pngjs");
const { Resvg } = require("@resvg/resvg-js");

const here = dirname(fileURLToPath(import.meta.url));
const mobile = join(here, "..");
const repo = join(mobile, "..", "..");
const desktopAssets = join(repo, DESKTOP_ASSETS);
const appAssets = join(mobile, "assets", "agents");
const menuIcons = join(mobile, "assets", "icons", "harness");

/** Pixel size of the app's PNGs: 3× the largest mark the app draws (about 40pt). */
const APP_SIZE = 128;
/** The menu mark's size in points, as `HarnessImage` draws it in the spawn rail. */
const MENU_POINTS = 20;
const MENU_SCALES = [1, 2, 3];

/** Box-filter resample of straight RGBA into premultiplied floats; scales either way. */
function resample(source, width, height) {
	const out = new Float64Array(width * height * 4);
	const rx = source.width / width;
	const ry = source.height / height;
	for (let y = 0; y < height; y++) {
		const y0 = y * ry, y1 = (y + 1) * ry;
		for (let x = 0; x < width; x++) {
			const x0 = x * rx, x1 = (x + 1) * rx;
			let r = 0, g = 0, b = 0, a = 0, area = 0;
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
			const o = (y * width + x) * 4;
			out[o] = r / area; out[o + 1] = g / area; out[o + 2] = b / area; out[o + 3] = a / area;
		}
	}
	return out;
}

/**
 * The SVG as resvg should read it. Brand files declare a wide-gamut colour after
 * its sRGB fallback (`stop-color:#0082fb;stop-color:color(display-p3 …)`). A
 * browser that cannot use the second keeps the first; resvg instead reads it as
 * black, which turned Muse's blue mark black. Dropping those declarations leaves
 * the fallback the desktop draws on an sRGB screen.
 */
function svgSource(file) {
	return readFileSync(join(desktopAssets, file), "utf8").replace(/;?\s*[\w-]+\s*:\s*color\([^)]*\)/g, "");
}

/** The mark's straight-RGBA pixels at `w`×`h`, from an SVG or PNG desktop asset. */
function draw(file, w, h) {
	if (extname(file) === ".svg") {
		const rendered = new Resvg(svgSource(file), { fitTo: { mode: "width", value: w }, font: { loadSystemFonts: false } }).render();
		const png = PNG.sync.read(rendered.asPng());
		// fitTo scales by width; the height follows the viewBox, so it matches `h` to a pixel.
		if (png.width === w && png.height === h) return png;
		return toStraight(resample(png, w, h), w, h);
	}
	return toStraight(resample(PNG.sync.read(readFileSync(join(desktopAssets, file))), w, h), w, h);
}

function toStraight(premultiplied, width, height) {
	const png = new PNG({ width, height });
	for (let i = 0; i < width * height; i++) {
		const a = premultiplied[i * 4 + 3];
		png.data[i * 4 + 3] = Math.round(a * 255);
		for (let c = 0; c < 3; c++) png.data[i * 4 + c] = a > 0 ? Math.round(Math.min(255, premultiplied[i * 4 + c] / a)) : 0;
	}
	return png;
}

/** The asset's aspect ratio (width / height), from its viewBox or pixel size. */
function aspect(file) {
	if (extname(file) === ".svg") {
		const svg = new Resvg(svgSource(file), { font: { loadSystemFonts: false } });
		return svg.width / svg.height;
	}
	const png = PNG.sync.read(readFileSync(join(desktopAssets, file)));
	return png.width / png.height;
}

/**
 * A `size`×`size` PNG with the mark contained and centred, like the desktop's
 * `object-contain` box: wide marks (amp's wordmark) keep their shape.
 */
function rasterise(file, size) {
	const ratio = aspect(file);
	const w = ratio >= 1 ? size : Math.max(1, Math.round(size * ratio));
	const h = ratio >= 1 ? Math.max(1, Math.round(size / ratio)) : size;
	const mark = draw(file, w, h);
	const out = new PNG({ width: size, height: size });
	const left = Math.floor((size - w) / 2);
	const top = Math.floor((size - h) / 2);
	for (let y = 0; y < h; y++) {
		mark.data.copy(out.data, ((y + top) * size + left) * 4, y * w * 4, (y + 1) * w * 4);
	}
	return PNG.sync.write(out, { colorType: 6 });
}

const assetName = (file) => file.slice(0, -extname(file).length);

const logos = readDesktopLogos(repo);
const keys = Object.keys(logos).sort();

// One PNG per desktop asset; harnesses that share an asset (opencode and
// opencode-v2) share the PNG too.
rmSync(appAssets, { recursive: true, force: true });
mkdirSync(appAssets, { recursive: true });
const files = [...new Set(keys.flatMap((key) => [logos[key].light, logos[key].dark].filter(Boolean)))].sort();
const hashes = {};
for (const file of files) {
	writeFileSync(join(appAssets, `${assetName(file)}.png`), rasterise(file, APP_SIZE));
	hashes[file] = sourceHash(readFileSync(join(desktopAssets, file)));
}

const generated = `// Generated by scripts/sync-harness-logos.mjs from the desktop's AgentAvatar.tsx. Do not edit;
// change the desktop's marks and run the script again.`;

const property = (key) => (/^[a-z_$][\w$]*$/i.test(key) ? key : JSON.stringify(key));
const fileEntry = (file) => `{ asset: "${assetName(file)}", source: "${file}", hash: "${hashes[file]}" }`;
const manifestLines = keys.map((key) => {
	const { light, dark } = logos[key];
	return `\t${property(key)}: { light: ${fileEntry(light)}${dark ? `, dark: ${fileEntry(dark)}` : ""} },`;
});
writeFileSync(
	join(mobile, "lib", "harnessLogoManifest.ts"),
	`${generated}

export type HarnessLogoFile = { asset: string; source: string; hash: string };

/** Each harness's mark: the app asset name, the desktop file it comes from, and that file's hash. */
export const HARNESS_LOGO_MANIFEST: Readonly<Record<string, { light: HarnessLogoFile; dark?: HarnessLogoFile }>> = {
${manifestLines.join("\n")}
};
`,
);

const req = (file) => `require("../assets/agents/${assetName(file)}.png")`;
const lines = keys.map((key) => {
	const { light, dark } = logos[key];
	const value = dark ? `{ light: ${req(light)}, dark: ${req(dark)} }` : `{ light: ${req(light)} }`;
	return `\t${property(key)}: ${value},`;
});
writeFileSync(
	join(mobile, "lib", "harnessLogoAssets.ts"),
	`${generated}
//
// The \`require\` half of the logo registry. Metro resolves these at build time, so
// they have to be literal, and Node cannot require a PNG, so lib/harnessLogo.ts
// reads lib/harnessLogoManifest.ts instead and stays unit-testable.

import { logoKey } from "./harnessLogo";

const LOGOS: Record<string, { light: number; dark?: number }> = {
${lines.join("\n")}
};

/**
 * The mark to draw for a harness on the given colour scheme, or null when it has
 * none. A brand that ships one mark for both backgrounds returns it for both.
 */
export function logoFor(harness: string | null | undefined, scheme: "light" | "dark"): number | null {
	const logo = LOGOS[logoKey(harness)];
	if (!logo) return null;
	return scheme === "dark" ? (logo.dark ?? logo.light) : logo.light;
}
`,
);

// SwiftUI menus draw marks by asset name. The dark mark is the image set's dark
// appearance, which iOS picks from the app's own colour scheme.
rmSync(menuIcons, { recursive: true, force: true });
for (const key of keys) {
	const set = join(menuIcons, `HarnessLogo-${key}.imageset`);
	mkdirSync(set, { recursive: true });
	const images = [];
	for (const [variant, file] of [["light", logos[key].light], ["dark", logos[key].dark]]) {
		if (!file) continue;
		for (const scale of MENU_SCALES) {
			const filename = `${key}${variant === "dark" ? "-dark" : ""}@${scale}x.png`;
			writeFileSync(join(set, filename), rasterise(file, MENU_POINTS * scale));
			images.push({
				...(variant === "dark" ? { appearances: [{ appearance: "luminosity", value: "dark" }] } : {}),
				filename,
				idiom: "universal",
				scale: `${scale}x`,
			});
		}
	}
	writeFileSync(
		join(set, "Contents.json"),
		`${JSON.stringify({ images, info: { author: "xcode", version: 1 }, properties: { "template-rendering-intent": "original" } }, null, 2)}\n`,
	);
}

const pairs = keys.filter((key) => logos[key].dark).length;
console.log(`Synced ${keys.length} harness marks (${pairs} with a dark variant) from ${relative(repo, desktopAssets)}.`);
