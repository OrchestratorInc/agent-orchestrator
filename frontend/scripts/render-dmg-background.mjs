#!/usr/bin/env node
// Renders assets/dmg/background.svg to the 1x and @2x (Retina) PNGs the macOS
// install window uses. electron-builder's dmg target merges the pair into one
// multi-resolution TIFF (tiffutil -cathidpicheck) when it finds the @2x file
// beside the 1x one, so both must exist and be exactly 1x/2x of the SVG size.
//
// Run on macOS from frontend/ after editing the SVG (or a harness logo it
// references), and commit the PNGs:
//   node scripts/render-dmg-background.mjs
// macOS so the SVG's ui-serif italic resolves to New York, Apple's system
// serif, as it would in Finder's own text.
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium } from "@playwright/test";

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), "../assets/dmg");
const source = path.join(dir, "background.svg");
const svg = await readFile(source, "utf8");
const [, width, height] = svg.match(/<svg[^>]*width="(\d+)"[^>]*height="(\d+)"/).map(Number);

const browser = await chromium.launch();
try {
	for (const [scale, name] of [
		[1, "background.png"],
		[2, "background@2x.png"],
	]) {
		const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: scale });
		// Open the file itself, not its text: the harness logos are relative
		// <image> references into src/renderer/assets/agents/. goto resolves on
		// the load event, which waits for those images.
		await page.goto(pathToFileURL(source).href);
		await page.screenshot({ path: path.join(dir, name), omitBackground: false });
		await page.close();
		console.log(`wrote ${name} (${width * scale}x${height * scale})`);
	}
} finally {
	await browser.close();
}
