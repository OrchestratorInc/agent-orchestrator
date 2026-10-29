#!/usr/bin/env node
// Renders assets/dmg/background.svg to the 1x and @2x (Retina) PNGs the macOS
// install window uses. electron-builder's dmg target merges the pair into one
// multi-resolution TIFF (tiffutil -cathidpicheck) when it finds the @2x file
// beside the 1x one, so both must exist and be exactly 1x/2x of the SVG size.
//
// Run on macOS from frontend/ after editing the SVG, and commit the PNGs:
//   node scripts/render-dmg-background.mjs
// macOS so the SVG's -apple-system font resolves to SF Pro, the face AO's
// design tokens use on macOS (DESIGN.md), and the only place a dmg is opened.
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), "../assets/dmg");
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
