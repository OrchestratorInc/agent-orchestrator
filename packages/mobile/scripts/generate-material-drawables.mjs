#!/usr/bin/env node
// Writes Material Symbols as Android vector drawables, for the native APIs that
// take an image source rather than a glyph: Stack.Toolbar items, Compose `Icon`
// and MenuView actions. The outlines come from the same Material Symbols face
// (weight 400, outlined) that AppSymbol draws from, so a header item and an
// inline glyph match exactly.
//
// Run from the repo with the mobile app installed, naming the symbols by their
// Android name (the `android` side of an entry in lib/symbols.ts):
//   node packages/mobile/scripts/generate-material-drawables.mjs open_in_new wrap_text
// Each one lands at packages/mobile/assets/icons/<name>.xml. Then add the name to
// MaterialDrawableName in lib/toolbarIcon.types.ts and its require to
// lib/toolbarIcon.android.ts.

import { readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const mobile = join(here, "..");
const require = createRequire(join(mobile, "package.json"));
const font = readFileSync(require.resolve("@expo-google-fonts/material-symbols/400Regular/MaterialSymbols_400Regular.ttf"));
const codepoints = JSON.parse(readFileSync(join(mobile, "node_modules/expo-symbols/build/android/symbols.json"), "utf8"));

const names = process.argv.slice(2);
if (names.length === 0) {
	console.error("Name at least one Material Symbol, e.g. open_in_new.");
	process.exit(1);
}

const tables = {};
for (let index = 0; index < font.readUInt16BE(4); index++) {
	const record = 12 + index * 16;
	tables[font.toString("ascii", record, record + 4)] = font.readUInt32BE(record + 8);
}
const unitsPerEm = font.readUInt16BE(tables.head + 18);
const longOffsets = font.readInt16BE(tables.head + 50) === 1;

// The Unicode full-repertoire cmap (format 12); every symbol sits in it.
function glyphIndex(codepoint) {
	for (let index = 0; index < font.readUInt16BE(tables.cmap + 2); index++) {
		const record = tables.cmap + 4 + index * 8;
		if (font.readUInt16BE(record) !== 3 || font.readUInt16BE(record + 2) !== 10) continue;
		const subtable = tables.cmap + font.readUInt32BE(record + 4);
		for (let group = 0; group < font.readUInt32BE(subtable + 12); group++) {
			const entry = subtable + 16 + group * 12;
			const start = font.readUInt32BE(entry);
			if (codepoint >= start && codepoint <= font.readUInt32BE(entry + 4)) return font.readUInt32BE(entry + 8) + codepoint - start;
		}
	}
	return 0;
}

function glyphRange(glyph) {
	if (longOffsets) return [font.readUInt32BE(tables.loca + glyph * 4), font.readUInt32BE(tables.loca + glyph * 4 + 4)];
	return [font.readUInt16BE(tables.loca + glyph * 2) * 2, font.readUInt16BE(tables.loca + glyph * 2 + 2) * 2];
}

// TrueType outlines: closed contours of on- and off-curve (quadratic) points.
function contours(glyph) {
	const [start, end] = glyphRange(glyph);
	if (start === end) return [];
	let at = tables.glyf + start;
	const count = font.readInt16BE(at);
	if (count < 0) return compound(at);
	at += 10;
	const ends = [];
	for (let index = 0; index < count; index++, at += 2) ends.push(font.readUInt16BE(at));
	const points = ends[count - 1] + 1;
	at += 2 + font.readUInt16BE(at);
	const flags = [];
	while (flags.length < points) {
		const flag = font[at++];
		flags.push(flag);
		if (flag & 8) for (let repeat = font[at++]; repeat > 0; repeat--) flags.push(flag);
	}
	const read = (short, same) => {
		const values = [];
		let value = 0;
		for (const flag of flags) {
			if (flag & short) value += flag & same ? font[at++] : -font[at++];
			else if (!(flag & same)) { value += font.readInt16BE(at); at += 2; }
			values.push(value);
		}
		return values;
	};
	const xs = read(2, 16);
	const ys = read(4, 32);
	const result = [];
	let first = 0;
	for (const last of ends) {
		const contour = [];
		for (let index = first; index <= last; index++) contour.push({ x: xs[index], y: ys[index], on: (flags[index] & 1) === 1 });
		result.push(contour);
		first = last + 1;
	}
	return result;
}

function compound(at) {
	at += 10;
	const result = [];
	for (let more = true; more;) {
		const flags = font.readUInt16BE(at);
		const glyph = font.readUInt16BE(at + 2);
		at += 4;
		if (!(flags & 2)) throw new Error("A compound glyph placed by matching points is not supported.");
		let dx, dy;
		if (flags & 1) { dx = font.readInt16BE(at); dy = font.readInt16BE(at + 2); at += 4; }
		else { dx = font.readInt8(at); dy = font.readInt8(at + 1); at += 2; }
		const f2dot14 = (offset) => font.readInt16BE(offset) / 16384;
		let [a, b, c, d] = [1, 0, 0, 1];
		if (flags & 8) { a = d = f2dot14(at); at += 2; }
		else if (flags & 0x40) { a = f2dot14(at); d = f2dot14(at + 2); at += 4; }
		else if (flags & 0x80) { [a, b, c, d] = [f2dot14(at), f2dot14(at + 2), f2dot14(at + 4), f2dot14(at + 6)]; at += 8; }
		for (const contour of contours(glyph)) result.push(contour.map(({ x, y, on }) => ({ x: a * x + c * y + dx, y: b * x + d * y + dy, on })));
		more = (flags & 0x20) !== 0;
	}
	return result;
}

// Font units grow upwards from the baseline; a drawable's viewport grows
// downwards from its top. Material's own drawables use the same 960 viewport.
const number = (value) => String(Math.round(value * 100) / 100);
const point = ({ x, y }) => `${number(x)},${number(unitsPerEm - y)}`;
const midpoint = (a, b) => ({ x: (a.x + b.x) / 2, y: (a.y + b.y) / 2, on: true });

function pathData(glyphContours) {
	let data = "";
	for (const contour of glyphContours) {
		if (contour.length === 0) continue;
		const firstOn = contour.findIndex((entry) => entry.on);
		const points = firstOn === -1
			? [midpoint(contour[0], contour[1]), ...contour.slice(1), contour[0]]
			: [...contour.slice(firstOn), ...contour.slice(0, firstOn)];
		data += `M${point(points[0])}`;
		for (let index = 1; index < points.length; index++) {
			const current = points[index];
			if (current.on) { data += `L${point(current)}`; continue; }
			const next = points[(index + 1) % points.length];
			data += `Q${point(current)} ${point(next.on ? next : midpoint(current, next))}`;
			if (next.on) index++;
		}
		data += "Z";
	}
	return data;
}

for (const name of names) {
	const codepoint = codepoints[name];
	if (codepoint === undefined) throw new Error(`"${name}" is not a Material Symbol name expo-symbols knows.`);
	const data = pathData(contours(glyphIndex(codepoint)));
	if (!data) throw new Error(`"${name}" has no outline in the font.`);
	const xml = `<vector xmlns:android="http://schemas.android.com/apk/res/android"
    android:width="24dp"
    android:height="24dp"
    android:viewportWidth="${unitsPerEm}"
    android:viewportHeight="${unitsPerEm}">
  <path
      android:fillColor="#FF000000"
      android:pathData="${data}"/>
</vector>
`;
	writeFileSync(join(mobile, "assets/icons", `${name}.xml`), xml);
	console.log(`assets/icons/${name}.xml`);
}
