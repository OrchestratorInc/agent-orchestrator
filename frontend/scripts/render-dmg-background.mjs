#!/usr/bin/env node
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
		await page.goto(pathToFileURL(source).href);
		await page.screenshot({ path: path.join(dir, name), omitBackground: false });
		await page.close();
		console.log(`wrote ${name} (${width * scale}x${height * scale})`);
	}
} finally {
	await browser.close();
}
