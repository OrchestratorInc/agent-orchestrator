#!/usr/bin/env node
// Writes a lucide icon as PNG bitmaps for a native bar icon, for a domain glyph
// the system symbol sets do not draw (the pull request, in the board header).
//
// NativeTabs takes a Material Symbol by name (`md`) or an image source (`src`).
// On Android react-native-screens decodes `src` with Fresco and keeps only a
// bitmap, so a vector drawable draws nothing there. The navigation bar tints the
// icon itself, so only the coverage matters; it is drawn in white, as NativeTabs
// draws its `md` glyphs.
//
// Run from the repo with the mobile app installed, naming the lucide icon and
// the file to write:
//   node packages/mobile/scripts/generate-lucide-tab-icon.mjs git-pull-request pull-request
// It writes packages/mobile/assets/icons/<file>.png at 24dp, plus the @1.5x to
// @4x densities Metro picks from.

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { crc32, deflateSync } from "node:zlib";

const here = dirname(fileURLToPath(import.meta.url));
const mobile = join(here, "..");
const [icon, output] = process.argv.slice(2);
if (!icon || !output) {
	console.error("Name a lucide icon and the file to write, e.g. git-pull-request pull-request.");
	process.exit(1);
}

// The icon's own data, read from the package rather than copied: lucide draws on
// a 24-unit grid with a 2-unit round stroke and no fill.
const module = readFileSync(join(mobile, "node_modules/lucide-react-native/dist/esm/icons", `${icon}.mjs`), "utf8");
const nodeSource = /node:\s*(\[[\s\S]*?\])\s*\n\s*\};/.exec(module)?.[1];
if (!nodeSource) throw new Error(`No icon data found for "${icon}".`);
const nodes = new Function(`return ${nodeSource}`)();
const GRID = 24;
const HALF_STROKE = 1;

// Every shape as what the stroke runs along: circles, and polylines (arcs are
// flattened into short segments, far finer than a pixel at any density).
const circles = [];
const segments = [];

function addArc(from, rx, ry, rotation, largeArc, sweep, to) {
	// SVG's endpoint-to-centre conversion (SVG 1.1, appendix F.6.5).
	const phi = (rotation * Math.PI) / 180;
	const [cos, sin] = [Math.cos(phi), Math.sin(phi)];
	const dx = (from.x - to.x) / 2;
	const dy = (from.y - to.y) / 2;
	const x1 = cos * dx + sin * dy;
	const y1 = -sin * dx + cos * dy;
	const scale = Math.max(1, Math.sqrt((x1 * x1) / (rx * rx) + (y1 * y1) / (ry * ry)));
	rx *= scale;
	ry *= scale;
	const numerator = rx * rx * ry * ry - rx * rx * y1 * y1 - ry * ry * x1 * x1;
	const root = Math.sqrt(Math.max(0, numerator / (rx * rx * y1 * y1 + ry * ry * x1 * x1)));
	const sign = largeArc === sweep ? -1 : 1;
	const cx1 = (sign * root * rx * y1) / ry;
	const cy1 = (sign * -root * ry * x1) / rx;
	const cx = cos * cx1 - sin * cy1 + (from.x + to.x) / 2;
	const cy = sin * cx1 + cos * cy1 + (from.y + to.y) / 2;
	const angle = (ux, uy, vx, vy) => Math.atan2(ux * vy - uy * vx, ux * vx + uy * vy);
	const start = angle(1, 0, (x1 - cx1) / rx, (y1 - cy1) / ry);
	let delta = angle((x1 - cx1) / rx, (y1 - cy1) / ry, (-x1 - cx1) / rx, (-y1 - cy1) / ry);
	if (!sweep && delta > 0) delta -= 2 * Math.PI;
	if (sweep && delta < 0) delta += 2 * Math.PI;
	const steps = 64;
	let previous = from;
	for (let step = 1; step <= steps; step++) {
		const theta = start + (delta * step) / steps;
		const point = step === steps ? to : {
			x: cos * rx * Math.cos(theta) - sin * ry * Math.sin(theta) + cx,
			y: sin * rx * Math.cos(theta) + cos * ry * Math.sin(theta) + cy,
		};
		segments.push([previous, point]);
		previous = point;
	}
}

function addPath(data) {
	const tokens = data.match(/[MmLlHhVvAaZz]|-?(?:\d+\.?\d*|\.\d+)(?:e[-+]?\d+)?/g) ?? [];
	let index = 0;
	let command = "";
	let current = { x: 0, y: 0 };
	let start = current;
	const next = () => Number(tokens[index++]);
	while (index < tokens.length) {
		if (/[A-Za-z]/.test(tokens[index])) command = tokens[index++];
		const relative = command === command.toLowerCase();
		const base = relative ? current : { x: 0, y: 0 };
		const lineTo = (point) => { segments.push([current, point]); current = point; };
		switch (command.toUpperCase()) {
			case "M": {
				current = start = { x: base.x + next(), y: base.y + next() };
				// Further pairs after a moveto are linetos.
				command = relative ? "l" : "L";
				break;
			}
			case "L": lineTo({ x: base.x + next(), y: base.y + next() }); break;
			case "H": lineTo({ x: (relative ? current.x : 0) + next(), y: current.y }); break;
			case "V": lineTo({ x: current.x, y: (relative ? current.y : 0) + next() }); break;
			case "A": {
				const [rx, ry, rotation, largeArc, sweep] = [next(), next(), next(), next(), next()];
				const to = { x: base.x + next(), y: base.y + next() };
				addArc(current, rx, ry, rotation, largeArc === 1, sweep === 1, to);
				current = to;
				break;
			}
			case "Z": lineTo(start); break;
			default: throw new Error(`Path command "${command}" in "${data}" is not supported.`);
		}
	}
}

for (const [type, attributes] of nodes) {
	if (type === "circle") circles.push({ x: Number(attributes.cx), y: Number(attributes.cy), r: Number(attributes.r) });
	else if (type === "path") addPath(attributes.d);
	else if (type === "line") segments.push([{ x: Number(attributes.x1), y: Number(attributes.y1) }, { x: Number(attributes.x2), y: Number(attributes.y2) }]);
	else throw new Error(`Element "${type}" is not supported.`);
}

// Distance to the nearest stroke centreline; a round-capped, round-joined
// stroke covers every point within half its width of it.
function distance(x, y) {
	let nearest = Infinity;
	for (const circle of circles) nearest = Math.min(nearest, Math.abs(Math.hypot(x - circle.x, y - circle.y) - circle.r));
	for (const [a, b] of segments) {
		const [vx, vy] = [b.x - a.x, b.y - a.y];
		const length = vx * vx + vy * vy;
		const t = length === 0 ? 0 : Math.max(0, Math.min(1, ((x - a.x) * vx + (y - a.y) * vy) / length));
		nearest = Math.min(nearest, Math.hypot(x - (a.x + t * vx), y - (a.y + t * vy)));
	}
	return nearest;
}

// Coverage from a 5x5 grid of samples per pixel, which is the antialiasing.
const SAMPLES = 5;
function render(size) {
	const pixels = Buffer.alloc(size * (size * 4 + 1));
	for (let row = 0; row < size; row++) {
		const line = row * (size * 4 + 1);
		pixels[line] = 0;
		for (let column = 0; column < size; column++) {
			let covered = 0;
			for (let sy = 0; sy < SAMPLES; sy++) {
				for (let sx = 0; sx < SAMPLES; sx++) {
					const x = ((column + (sx + 0.5) / SAMPLES) * GRID) / size;
					const y = ((row + (sy + 0.5) / SAMPLES) * GRID) / size;
					if (distance(x, y) <= HALF_STROKE) covered++;
				}
			}
			const at = line + 1 + column * 4;
			pixels.fill(255, at, at + 3);
			pixels[at + 3] = Math.round((covered / (SAMPLES * SAMPLES)) * 255);
		}
	}
	return pixels;
}

function chunk(type, data) {
	const length = Buffer.alloc(4);
	length.writeUInt32BE(data.length);
	const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
	const crc = Buffer.alloc(4);
	crc.writeUInt32BE(crc32(body));
	return Buffer.concat([length, body, crc]);
}

function png(size) {
	const header = Buffer.alloc(13);
	header.writeUInt32BE(size, 0);
	header.writeUInt32BE(size, 4);
	header[8] = 8; // bits per channel
	header[9] = 6; // RGBA
	return Buffer.concat([
		Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
		chunk("IHDR", header),
		chunk("IDAT", deflateSync(render(size), { level: 9 })),
		chunk("IEND", Buffer.alloc(0)),
	]);
}

const DP = 24;
for (const density of [1, 1.5, 2, 3, 4]) {
	const file = `${output}${density === 1 ? "" : `@${density}x`}.png`;
	writeFileSync(join(mobile, "assets/icons", file), png(DP * density));
	console.log(`assets/icons/${file}`);
}
