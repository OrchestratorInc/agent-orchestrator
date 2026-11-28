import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { patchWebmDuration, setWebmDuration } from "./webm-duration";

// Minimal EBML writing for the shape Chromium's MediaRecorder produces.
const id = (value: number) => {
	const bytes: number[] = [];
	for (let rest = value; rest > 0; rest = Math.floor(rest / 256)) bytes.unshift(rest & 0xff);
	return Buffer.from(bytes);
};
const size1 = (length: number) => Buffer.from([0x80 | length]);
const el = (elementId: number, body: Buffer) => Buffer.concat([id(elementId), size1(body.length), body]);
const UNKNOWN_SIZE = Buffer.from([0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff]);

function mediaRecorderWebm(): Buffer {
	const header = el(0x1a45dfa3, el(0x4282, Buffer.from("webm")));
	const info = el(0x1549a966, Buffer.concat([el(0x2ad7b1, Buffer.from([0x0f, 0x42, 0x40])), el(0x4d80, Buffer.from("Chrome"))]));
	const tracks = el(0x1654ae6b, Buffer.from([0xae, 0x80]));
	const cluster = Buffer.from([0x1f, 0x43, 0xb6, 0x75, 0x84, 1, 2, 3, 4]);
	return Buffer.concat([header, id(0x18538067), UNKNOWN_SIZE, info, tracks, cluster]);
}

function readDuration(file: Buffer): number | undefined {
	const at = file.indexOf(Buffer.from([0x44, 0x89]));
	if (at < 0) return undefined;
	// 8-byte size field, then an 8-byte float.
	return file.readDoubleBE(at + 2 + 8);
}

describe("webm duration", () => {
	it("adds Duration to the Segment Info and keeps everything after it", () => {
		const original = mediaRecorderWebm();
		const patch = patchWebmDuration(original, 4_250)!;
		expect(patch).not.toBeNull();
		const patched = Buffer.concat([patch.head, original.subarray(patch.replaced)]);
		expect(readDuration(patched)).toBe(4_250);
		// The original Info children are kept, and the tracks and clusters follow.
		expect(patched.includes(Buffer.from("Chrome"))).toBe(true);
		expect(patched.subarray(-9)).toEqual(original.subarray(-9));
	});

	it("replaces a Duration already there", () => {
		const first = mediaRecorderWebm();
		const once = patchWebmDuration(first, 1_000)!;
		const patched = Buffer.concat([once.head, first.subarray(once.replaced)]);
		const twice = patchWebmDuration(patched, 2_000)!;
		const repatched = Buffer.concat([twice.head, patched.subarray(twice.replaced)]);
		expect(readDuration(repatched)).toBe(2_000);
		expect(repatched.indexOf(Buffer.from([0x44, 0x89]))).toBe(repatched.lastIndexOf(Buffer.from([0x44, 0x89])));
	});

	it("leaves anything else alone", () => {
		expect(patchWebmDuration(Buffer.from("not a webm file"), 1_000)).toBeNull();
		expect(patchWebmDuration(mediaRecorderWebm(), Number.NaN)).toBeNull();
	});

	it("rewrites a file in place", async () => {
		const file = path.join(mkdtempSync(path.join(tmpdir(), "ao-webm-")), "r.webm");
		writeFileSync(file, mediaRecorderWebm());
		await expect(setWebmDuration(file, 3_000)).resolves.toBe(true);
		expect(readDuration(readFileSync(file))).toBe(3_000);
	});
});
