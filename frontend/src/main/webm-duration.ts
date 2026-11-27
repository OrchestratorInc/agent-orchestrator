import { createReadStream, createWriteStream } from "node:fs";
import { open, rename, rm } from "node:fs/promises";
import { pipeline } from "node:stream/promises";

// Chromium's MediaRecorder writes WebM for live streaming: its Segment Info has
// no Duration, so players show no length and cannot seek. This writes the
// Duration into the Segment Info of a finished recording.

const SEGMENT_ID = 0x18538067;
const INFO_ID = 0x1549a966;
const TIMECODE_SCALE_ID = 0x2ad7b1;
const DURATION_ID = 0x4489;
// Info sits right after the EBML header; it is never this far in.
const HEAD_BYTES = 64 * 1024;

type Vint = { value: number; length: number; unknown: boolean };

function readVint(data: Buffer, offset: number, keepMarker: boolean): Vint | null {
	const first = data[offset];
	if (first === undefined || first === 0) return null;
	let length = 1;
	while (length <= 8 && (first & (0x80 >> (length - 1))) === 0) length += 1;
	if (length > 8 || offset + length > data.length) return null;
	let value = keepMarker ? first : first & (0xff >> length);
	let allOnes = value === (0xff >> length);
	for (let index = 1; index < length; index += 1) {
		const byte = data[offset + index]!;
		value = value * 256 + byte;
		if (byte !== 0xff) allOnes = false;
	}
	return { value, length, unknown: !keepMarker && allOnes };
}

function encodeId(id: number): Buffer {
	const bytes: number[] = [];
	for (let rest = id; rest > 0; rest = Math.floor(rest / 256)) bytes.unshift(rest & 0xff);
	return Buffer.from(bytes);
}

// Sizes are always written with 8 bytes so the patch never depends on the old width.
function encodeSize(size: number): Buffer {
	const out = Buffer.alloc(8);
	out[0] = 0x01;
	let rest = size;
	for (let index = 7; index >= 1; index -= 1) {
		out[index] = rest & 0xff;
		rest = Math.floor(rest / 256);
	}
	return out;
}

function element(id: number, body: Buffer): Buffer {
	return Buffer.concat([encodeId(id), encodeSize(body.length), body]);
}

/**
 * The start of a WebM file with Duration set, and how many bytes of the
 * original start it replaces; null when the start is not a WebM this can patch.
 */
export function patchWebmDuration(head: Buffer, durationMs: number): { head: Buffer; replaced: number } | null {
	if (!Number.isFinite(durationMs) || durationMs < 0) return null;
	// EBML header.
	const headerId = readVint(head, 0, true);
	const headerSize = headerId && readVint(head, headerId.length, false);
	if (!headerId || headerId.value !== 0x1a45dfa3 || !headerSize) return null;
	let offset = headerId.length + headerSize.length + headerSize.value;
	const segmentId = readVint(head, offset, true);
	const segmentSize = segmentId && readVint(head, offset + segmentId.length, false);
	if (!segmentId || segmentId.value !== SEGMENT_ID || !segmentSize) return null;
	// A known-size Segment would need its size grown too; MediaRecorder never writes one.
	if (!segmentSize.unknown) return null;
	offset += segmentId.length + segmentSize.length;
	while (offset < head.length) {
		const id = readVint(head, offset, true);
		const size = id && readVint(head, offset + id.length, false);
		if (!id || !size || size.unknown) return null;
		const bodyStart = offset + id.length + size.length;
		const bodyEnd = bodyStart + size.value;
		if (id.value !== INFO_ID) {
			offset = bodyEnd;
			continue;
		}
		if (bodyEnd > head.length) return null;
		const children: Buffer[] = [];
		let timecodeScale = 1_000_000;
		let cursor = bodyStart;
		while (cursor < bodyEnd) {
			const childId = readVint(head, cursor, true);
			const childSize = childId && readVint(head, cursor + childId.length, false);
			if (!childId || !childSize) return null;
			const childEnd = cursor + childId.length + childSize.length + childSize.value;
			if (childEnd > bodyEnd) return null;
			if (childId.value === TIMECODE_SCALE_ID) {
				const valueStart = cursor + childId.length + childSize.length;
				let scale = 0;
				for (let index = valueStart; index < childEnd; index += 1) scale = scale * 256 + head[index]!;
				if (scale > 0) timecodeScale = scale;
			}
			if (childId.value !== DURATION_ID) children.push(head.subarray(cursor, childEnd));
			cursor = childEnd;
		}
		const duration = Buffer.alloc(8);
		// Duration counts TimecodeScale units (nanoseconds each).
		duration.writeDoubleBE((durationMs * 1_000_000) / timecodeScale);
		const info = element(INFO_ID, Buffer.concat([...children, element(DURATION_ID, duration)]));
		return { head: Buffer.concat([head.subarray(0, offset), info]), replaced: bodyEnd };
	}
	return null;
}

/** Write Duration into a finished WebM file in place. False when it could not. */
export async function setWebmDuration(path: string, durationMs: number): Promise<boolean> {
	const file = await open(path, "r");
	let head: Buffer;
	try {
		const buffer = Buffer.alloc(HEAD_BYTES);
		const { bytesRead } = await file.read(buffer, 0, HEAD_BYTES, 0);
		head = buffer.subarray(0, bytesRead);
	} finally {
		await file.close();
	}
	const patch = patchWebmDuration(head, durationMs);
	if (!patch) return false;
	const temporary = `${path}.duration`;
	try {
		const out = createWriteStream(temporary, { mode: 0o600 });
		out.write(patch.head);
		await pipeline(createReadStream(path, { start: patch.replaced }), out);
		await rename(temporary, path);
		return true;
	} catch (error) {
		await rm(temporary, { force: true });
		throw error;
	}
}
