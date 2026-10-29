#!/usr/bin/env node
// Renders the macOS install window assets in assets/dmg/:
//
//   background.png, background@2x.png   from background.svg. electron-builder's
//     dmg target merges the pair into one multi-resolution TIFF (tiffutil
//     -cathidpicheck) when it finds the @2x file beside the 1x one, so both
//     must exist and be exactly 1x/2x of the SVG size.
//   volume.icns   the mounted volume's icon, from assets/icon.png. Built with
//     iconutil so the 16/32px sizes use the ic04/ic05 types Finder decodes;
//     assets/icon.icns keeps them as PNG in icp4/icp5, which renders as noise
//     in the window title bar.
//
// Run on macOS from frontend/ after editing the SVG or the app icon, and commit
// the outputs:
//   node scripts/render-dmg-assets.mjs
// macOS so the SVG's -apple-system font resolves to SF Pro, the face AO's
// design tokens use on macOS (DESIGN.md), and because iconutil and sips are
// macOS tools.
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { chromium } from "@playwright/test";

const run = promisify(execFile);
const assets = path.join(path.dirname(fileURLToPath(import.meta.url)), "../assets");
const dir = path.join(assets, "dmg");
const svg = await readFile(path.join(dir, "background.svg"), "utf8");
const [, width, height] = svg.match(/<svg[^>]*width="(\d+)"[^>]*height="(\d+)"/).map(Number);

const browser = await chromium.launch();
try {
	for (const [scale, name] of [
		[1, "background.png"],
		[2, "background@2x.png"],
	]) {
		const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: scale });
		await page.setContent(`<!doctype html><style>html,body{margin:0}svg{display:block}</style>${svg}`);
		await page.screenshot({ path: path.join(dir, name), omitBackground: false });
		await page.close();
		console.log(`wrote ${name} (${width * scale}x${height * scale})`);
	}
} finally {
	await browser.close();
}

const work = await mkdtemp(path.join(tmpdir(), "ao-volume-icon-"));
try {
	const iconset = path.join(work, "volume.iconset");
	await run("mkdir", [iconset]);
	for (const size of [16, 32, 128, 256, 512]) {
		for (const [scale, suffix] of [
			[1, ""],
			[2, "@2x"],
		]) {
			const px = String(size * scale);
			await run("sips", ["-z", px, px, path.join(assets, "icon.png"), "--out", path.join(iconset, `icon_${size}x${size}${suffix}.png`)]);
		}
	}
	await run("iconutil", ["-c", "icns", iconset, "-o", path.join(dir, "volume.icns")]);
	console.log("wrote volume.icns");
} finally {
	await rm(work, { recursive: true, force: true });
}
