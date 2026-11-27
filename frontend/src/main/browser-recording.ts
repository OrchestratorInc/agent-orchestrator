import { AoError } from "../../../packages/shared/telemetry/ao-error";
import { randomUUID } from "node:crypto";
import { mkdir, readdir } from "node:fs/promises";
import { open, rm, stat } from "node:fs/promises";
import path from "node:path";
import type { BrowserViewportSize } from "../shared/browser-device-presets";
import type { BrowserLiveFrame } from "./browser-live-types";
import { setWebmDuration } from "./webm-duration";

// `ao browser record`: a video of a session's browser page, made from the same
// page frames the mobile live view streams. Frames go to an encoder (a hidden
// recorder page in the app, browser-recording-encoder.ts) that writes WebM to
// a file under the app's data directory. The CLI reads the finished file back
// in chunks, so it lands on the agent's machine even when that is another host.

export type BrowserRecordingEncoder = {
	/** Queue one JPEG frame. The encoder may drop it when it is behind. */
	frame(jpeg: Buffer): void;
	/** Encode what is queued and complete the file. */
	finish(): Promise<void>;
	/** Stop without a file. */
	abort(): Promise<void>;
};

export type CreateBrowserRecordingEncoder = (
	file: string,
	onWritten: (bytes: number) => void,
) => Promise<BrowserRecordingEncoder>;

export type BrowserRecordingStopReason = "stopped" | "max-duration" | "max-size" | "session-closed" | "failed";

export type BrowserRecordingStatus = {
	recording: boolean;
	id?: string;
	durationMs?: number;
	frames?: number;
	width?: number;
	height?: number;
	fps?: number;
	/** The viewport the page is laid out at for this recording, when one was asked for. */
	viewport?: BrowserViewportSize;
};

export type BrowserRecordingResult = {
	id: string;
	bytes: number;
	durationMs: number;
	frames: number;
	width: number;
	height: number;
	mimeType: "video/webm";
	stopReason: BrowserRecordingStopReason;
	viewport?: BrowserViewportSize;
};

export type BrowserRecordingChunk = {
	id: string;
	offset: number;
	/** Base64 of the bytes from offset. */
	data: string;
	nextOffset: number;
	bytes: number;
	done: boolean;
};

export const DEFAULT_RECORDING_FPS = 10;
export const MAX_RECORDING_FPS = 30;
export const DEFAULT_RECORDING_MAX_SECONDS = 600;
export const MAX_RECORDING_MAX_SECONDS = 1800;
export const MAX_RECORDING_BYTES = 500 << 20;
// Read back in pieces that fit one runtime message (8 MiB) as base64 JSON.
export const RECORDING_CHUNK_BYTES = 3 << 20;
// A finished recording nobody reads is removed after this long.
const FINISHED_RECORDING_TTL_MS = 30 * 60_000;

type Clock = {
	now: () => number;
	setTimeout: (callback: () => void, ms: number) => ReturnType<typeof setTimeout>;
	clearTimeout: (timer: ReturnType<typeof setTimeout>) => void;
};
// Looked up on each call, not captured once.
const systemClock: Clock = {
	now: () => Date.now(),
	setTimeout: (callback, ms) => setTimeout(callback, ms),
	clearTimeout: (timer) => clearTimeout(timer),
};

function recordingError(code: string, message: string): AoError {
	return new AoError(code, message);
}

function sameShape(frame: { width: number; height: number }, viewport: { width: number; height: number }): boolean {
	return Math.abs(frame.width / frame.height - viewport.width / viewport.height) <= 0.02 * (viewport.width / viewport.height);
}

/** One recording in progress. */
export class BrowserRecording {
	readonly id: string;
	readonly file: string;
	private readonly startedAt: number;
	private readonly minFrameGapMs: number;
	private lastFrameAt = Number.NEGATIVE_INFINITY;
	private pending?: BrowserLiveFrame;
	private pendingTimer?: ReturnType<typeof setTimeout>;
	private limitTimer?: ReturnType<typeof setTimeout>;
	private frames = 0;
	private width = 0;
	private height = 0;
	private stopping?: Promise<BrowserRecordingResult>;

	constructor(
		private readonly encoder: BrowserRecordingEncoder,
		options: {
			id: string;
			file: string;
			fps: number;
			viewport?: BrowserViewportSize;
			maxDurationMs: number;
			/** The recording stopped itself (a limit); the owner should finish it. */
			onLimit: (reason: BrowserRecordingStopReason) => void;
			clock?: Clock;
		},
		private readonly clock: Clock = options.clock ?? systemClock,
	) {
		this.id = options.id;
		this.viewport = options.viewport;
		this.file = options.file;
		this.startedAt = this.clock.now();
		this.minFrameGapMs = 1000 / options.fps;
		this.fps = options.fps;
		this.limitTimer = this.clock.setTimeout(() => options.onLimit("max-duration"), options.maxDurationMs);
		this.limitTimer.unref?.();
	}

	readonly fps: number;
	readonly viewport?: BrowserViewportSize;

	/** A page frame. Frames beyond the frame rate wait for the next slot; only the newest waits. */
	frame(frame: BrowserLiveFrame): void {
		if (this.stopping) return;
		// At a chosen size, a frame of another shape is the page still resizing
		// into it (or out of it): it would set the video's size wrong.
		if (this.viewport && !sameShape(frame, this.viewport)) return;
		const now = this.clock.now();
		const wait = this.lastFrameAt + this.minFrameGapMs - now;
		if (wait <= 0) {
			this.emit(frame, now);
			return;
		}
		this.pending = frame;
		if (this.pendingTimer) return;
		this.pendingTimer = this.clock.setTimeout(() => {
			this.pendingTimer = undefined;
			const next = this.pending;
			this.pending = undefined;
			if (next && !this.stopping) this.emit(next, this.clock.now());
		}, wait);
	}

	private emit(frame: BrowserLiveFrame, at: number): void {
		this.lastFrameAt = at;
		this.frames += 1;
		if (!this.width) {
			this.width = frame.width;
			this.height = frame.height;
		}
		this.encoder.frame(frame.jpeg);
	}

	status(): BrowserRecordingStatus {
		return {
			recording: !this.stopping,
			id: this.id,
			durationMs: this.clock.now() - this.startedAt,
			frames: this.frames,
			width: this.width,
			height: this.height,
			fps: this.fps,
			...(this.viewport ? { viewport: this.viewport } : {}),
		};
	}

	/** Finish the file. Repeated calls share the first. */
	stop(reason: BrowserRecordingStopReason): Promise<BrowserRecordingResult> {
		this.stopping ??= this.finish(reason);
		return this.stopping;
	}

	private async finish(reason: BrowserRecordingStopReason): Promise<BrowserRecordingResult> {
		if (this.limitTimer) this.clock.clearTimeout(this.limitTimer);
		if (this.pendingTimer) this.clock.clearTimeout(this.pendingTimer);
		this.pendingTimer = undefined;
		// The page's last state belongs in the video.
		if (this.pending) this.emit(this.pending, this.clock.now());
		this.pending = undefined;
		const durationMs = this.clock.now() - this.startedAt;
		if (this.frames === 0) {
			await this.encoder.abort();
			await rm(this.file, { force: true });
			throw recordingError("BROWSER_RECORDING_EMPTY", "The recording captured no page frames");
		}
		try {
			await this.encoder.finish();
		} catch (error) {
			await rm(this.file, { force: true });
			throw error;
		}
		// Players need the length to show it and seek; the video plays without it.
		await setWebmDuration(this.file, durationMs).catch(() => false);
		const { size } = await stat(this.file);
		return {
			id: this.id,
			bytes: size,
			durationMs,
			frames: this.frames,
			width: this.width,
			height: this.height,
			mimeType: "video/webm",
			stopReason: reason,
			...(this.viewport ? { viewport: this.viewport } : {}),
		};
	}
}

/**
 * Finished recordings waiting to be read back, by id. Reading the last chunk
 * removes the file; one nobody reads is removed after a while.
 */
export class BrowserRecordingStore {
	private readonly finished = new Map<string, { file: string; bytes: number; timer: ReturnType<typeof setTimeout> }>();

	constructor(
		readonly directory: string,
		private readonly clock: Clock = systemClock,
	) {}

	/** A new recording's id. */
	newId(): string {
		return randomUUID();
	}

	/** Make the directory, removing recordings an earlier run left behind. */
	async prepare(): Promise<void> {
		await mkdir(this.directory, { recursive: true, mode: 0o700 });
		const names = await readdir(this.directory).catch(() => [] as string[]);
		await Promise.all(
			names
				.filter((name) => name.endsWith(".webm") && ![...this.finished.values()].some((entry) => path.basename(entry.file) === name))
				.map((name) => rm(path.join(this.directory, name), { force: true })),
		);
	}

	fileFor(id: string): string {
		return path.join(this.directory, `${id}.webm`);
	}

	keep(result: BrowserRecordingResult): void {
		const timer = this.clock.setTimeout(() => void this.remove(result.id), FINISHED_RECORDING_TTL_MS);
		timer.unref?.();
		this.finished.set(result.id, { file: this.fileFor(result.id), bytes: result.bytes, timer });
	}

	async read(id: string, offset: number): Promise<BrowserRecordingChunk> {
		const entry = this.finished.get(id);
		if (!entry) throw recordingError("BROWSER_RECORDING_NOT_FOUND", "No finished recording with that id; stop a recording first");
		if (!Number.isSafeInteger(offset) || offset < 0 || offset > entry.bytes) {
			throw recordingError("INVALID_ARGUMENT", "offset is outside the recording");
		}
		const length = Math.min(RECORDING_CHUNK_BYTES, entry.bytes - offset);
		const buffer = Buffer.alloc(length);
		const file = await open(entry.file, "r");
		try {
			await file.read(buffer, 0, length, offset);
		} finally {
			await file.close();
		}
		const nextOffset = offset + length;
		const done = nextOffset >= entry.bytes;
		if (done) await this.remove(id);
		return { id, offset, data: buffer.toString("base64"), nextOffset, bytes: entry.bytes, done };
	}

	async remove(id: string): Promise<void> {
		const entry = this.finished.get(id);
		if (!entry) return;
		this.finished.delete(id);
		this.clock.clearTimeout(entry.timer);
		await rm(entry.file, { force: true });
	}

	async dispose(): Promise<void> {
		await Promise.all([...this.finished.keys()].map((id) => this.remove(id)));
	}
}
