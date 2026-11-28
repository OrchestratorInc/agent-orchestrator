import { mkdtempSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BrowserLiveFrame } from "./browser-live-types";
import { BrowserRecording, BrowserRecordingStore, RECORDING_CHUNK_BYTES, type BrowserRecordingEncoder } from "./browser-recording";

const frame = (n: number, width = 1280, height = 720): BrowserLiveFrame => ({
	sequence: BigInt(n),
	capturedAtMs: BigInt(n),
	width,
	height,
	jpeg: Buffer.from(`jpeg-${n}`),
});

function fakeEncoder(file: string): BrowserRecordingEncoder & { frames: string[] } {
	const frames: string[] = [];
	return {
		frames,
		frame: (jpeg) => frames.push(jpeg.toString()),
		finish: vi.fn(async () => writeFileSync(file, "not-really-webm")),
		abort: vi.fn(async () => undefined),
	};
}

describe("BrowserRecording", () => {
	let directory: string;
	beforeEach(() => {
		vi.useFakeTimers({ now: 1_000_000 });
		directory = mkdtempSync(path.join(tmpdir(), "ao-recording-"));
	});
	afterEach(() => vi.useRealTimers());

	const make = (fps = 10, maxDurationMs = 60_000) => {
		const file = path.join(directory, "r1.webm");
		const encoder = fakeEncoder(file);
		const onLimit = vi.fn();
		const recording = new BrowserRecording(encoder, { id: "r1", file, fps, maxDurationMs, onLimit });
		return { encoder, onLimit, recording };
	};

	it("keeps at most the frame rate, holding only the newest frame for the next slot", () => {
		const { encoder, recording } = make(10);
		recording.frame(frame(1));
		recording.frame(frame(2));
		recording.frame(frame(3));
		expect(encoder.frames).toEqual(["jpeg-1"]);
		vi.advanceTimersByTime(100);
		expect(encoder.frames).toEqual(["jpeg-1", "jpeg-3"]);
		vi.advanceTimersByTime(500);
		recording.frame(frame(4));
		expect(encoder.frames).toEqual(["jpeg-1", "jpeg-3", "jpeg-4"]);
		expect(recording.status()).toMatchObject({ recording: true, id: "r1", frames: 3, width: 1280, height: 720, fps: 10 });
	});

	it("puts the page's last frame in the video when it stops", async () => {
		const { encoder, recording } = make(10);
		recording.frame(frame(1));
		recording.frame(frame(2));
		vi.advanceTimersByTime(40);
		const result = await recording.stop("stopped");
		expect(encoder.frames).toEqual(["jpeg-1", "jpeg-2"]);
		expect(encoder.finish).toHaveBeenCalledTimes(1);
		expect(result).toMatchObject({ id: "r1", frames: 2, durationMs: 40, mimeType: "video/webm", stopReason: "stopped" });
		expect(result.bytes).toBe("not-really-webm".length);
		// Stopping again answers the same result; later frames are not taken.
		recording.frame(frame(3));
		await expect(recording.stop("stopped")).resolves.toBe(result);
		expect(encoder.finish).toHaveBeenCalledTimes(1);
	});

	it("at a chosen size, skips frames of another shape", () => {
		const file = path.join(directory, "r1.webm");
		const encoder = fakeEncoder(file);
		const recording = new BrowserRecording(encoder, {
			id: "r1",
			file,
			fps: 10,
			maxDurationMs: 60_000,
			viewport: { width: 1440, height: 900, mobile: false },
			onLimit: vi.fn(),
		});
		recording.frame(frame(1, 734, 900));
		expect(encoder.frames).toEqual([]);
		recording.frame(frame(2, 1440, 900));
		expect(encoder.frames).toEqual(["jpeg-2"]);
		expect(recording.status()).toMatchObject({ width: 1440, height: 900, viewport: { width: 1440, height: 900 } });
	});

	it("stops by itself at its time limit", () => {
		const { onLimit } = make(10, 5_000);
		vi.advanceTimersByTime(5_000);
		expect(onLimit).toHaveBeenCalledWith("max-duration");
	});

	it("fails without a file when no frame arrived", async () => {
		const { encoder, recording } = make();
		await expect(recording.stop("stopped")).rejects.toMatchObject({ code: "BROWSER_RECORDING_EMPTY" });
		expect(encoder.abort).toHaveBeenCalled();
		expect(readdirSync(directory)).toEqual([]);
	});
});

describe("BrowserRecordingStore", () => {
	it("reads a finished recording back in chunks and removes it after the last", async () => {
		const directory = mkdtempSync(path.join(tmpdir(), "ao-recording-store-"));
		const store = new BrowserRecordingStore(directory);
		const bytes = RECORDING_CHUNK_BYTES + 10;
		writeFileSync(store.fileFor("r1"), Buffer.alloc(bytes, 7));
		store.keep({ id: "r1", bytes, durationMs: 1, frames: 1, width: 1, height: 1, mimeType: "video/webm", stopReason: "stopped" });

		const first = await store.read("r1", 0);
		expect(first).toMatchObject({ offset: 0, nextOffset: RECORDING_CHUNK_BYTES, bytes, done: false });
		expect(Buffer.from(first.data, "base64").length).toBe(RECORDING_CHUNK_BYTES);
		const last = await store.read("r1", first.nextOffset);
		expect(last).toMatchObject({ nextOffset: bytes, done: true });
		expect(Buffer.from(last.data, "base64").length).toBe(10);
		expect(readdirSync(directory)).toEqual([]);
		await expect(store.read("r1", 0)).rejects.toMatchObject({ code: "BROWSER_RECORDING_NOT_FOUND" });
	});

	it("refuses an offset outside the recording", async () => {
		const directory = mkdtempSync(path.join(tmpdir(), "ao-recording-store-"));
		const store = new BrowserRecordingStore(directory);
		writeFileSync(store.fileFor("r1"), "abc");
		store.keep({ id: "r1", bytes: 3, durationMs: 1, frames: 1, width: 1, height: 1, mimeType: "video/webm", stopReason: "stopped" });
		await expect(store.read("r1", 4)).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		await expect(store.read("r1", -1)).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		await store.dispose();
	});

	it("clears recordings an earlier run left behind", async () => {
		const directory = mkdtempSync(path.join(tmpdir(), "ao-recording-store-"));
		writeFileSync(path.join(directory, "old.webm"), "x");
		writeFileSync(path.join(directory, "notes.txt"), "keep");
		await new BrowserRecordingStore(directory).prepare();
		expect(readdirSync(directory)).toEqual(["notes.txt"]);
	});
});
