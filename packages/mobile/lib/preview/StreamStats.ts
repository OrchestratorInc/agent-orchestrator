// Measures the browser stream as the phone sees it, for the debug HUD and the
// baselines the renderer work is judged against: time to the first frame,
// frames shown and kilobytes received per second over the last second, decode
// time per shown frame, and input to the next frame shown. Every frame that
// arrives costs bandwidth, including one replaced before it was shown. Times are milliseconds on one clock (the
// caller's), so tests drive it without timers.

export type StreamStatsSnapshot = {
	firstFrameMs: number | null;
	fps: number;
	kbps: number;
	decodeMs: number | null;
	inputToFrameMs: number | null;
	frames: number;
};

const WINDOW_MS = 1_000;
// The rate window is kept as counts per BUCKET_MS, so it holds every frame of
// the last second however many arrive, in a bounded number of buckets.
const BUCKET_MS = 100;
const MAX_BUCKETS = WINDOW_MS / BUCKET_MS + 1;
// Enough for the medians below.
const MAX_SAMPLES = 32;

type Bucket = { start: number; frames: number; bytes: number };

// Adds to the bucket for `now`, starting one when time has moved on.
function count(buckets: Bucket[], now: number, bytes: number): void {
	const start = Math.floor(now / BUCKET_MS) * BUCKET_MS;
	let last = buckets[buckets.length - 1];
	if (!last || last.start !== start) {
		last = { start, frames: 0, bytes: 0 };
		buckets.push(last);
		if (buckets.length > MAX_BUCKETS) buckets.splice(0, buckets.length - MAX_BUCKETS);
	}
	last.frames++;
	last.bytes += bytes;
}

function median(values: number[]): number | null {
	if (!values.length) return null;
	const sorted = [...values].sort((a, b) => a - b);
	const mid = sorted.length >> 1;
	return sorted.length % 2 ? sorted[mid]! : (sorted[mid - 1]! + sorted[mid]!) / 2;
}

function push<T>(list: T[], value: T, max: number): void {
	list.push(value);
	if (list.length > max) list.splice(0, list.length - max);
}

export class StreamStats {
	private connectedAt: number | null = null;
	private firstFrameMs: number | null = null;
	private arrivals: Bucket[] = [];
	private shownAt: Bucket[] = [];
	private decodes: number[] = [];
	private latencies: number[] = [];
	// The earliest input no frame has answered yet.
	private pendingInput: number | null = null;
	private frames = 0;

	/** A (re)connect: the next frame is a first frame again. */
	connecting(now: number): void {
		this.connectedAt = now;
		this.firstFrameMs = null;
		this.pendingInput = null;
	}

	input(now: number): void {
		this.pendingInput ??= now;
	}

	/** A frame that arrived at `now`, shown or not: its size in bytes. */
	received(now: number, bytes: number): void {
		count(this.arrivals, now, bytes);
	}

	/** A frame shown at `now`, decodeMs after it arrived. */
	shown(now: number, decodeMs: number): void {
		this.frames++;
		if (this.firstFrameMs === null && this.connectedAt !== null) this.firstFrameMs = now - this.connectedAt;
		count(this.shownAt, now, 0);
		push(this.decodes, decodeMs, MAX_SAMPLES);
		if (this.pendingInput !== null) {
			push(this.latencies, now - this.pendingInput, MAX_SAMPLES);
			this.pendingInput = null;
		}
	}

	snapshot(now: number): StreamStatsSnapshot {
		// Buckets that started within the last second (to BUCKET_MS).
		const since = now - WINDOW_MS;
		const recent = (buckets: Bucket[]) => buckets.filter((b) => b.start >= since);
		return {
			firstFrameMs: this.firstFrameMs,
			fps: recent(this.shownAt).reduce((sum, b) => sum + b.frames, 0),
			kbps: Math.round(recent(this.arrivals).reduce((sum, b) => sum + b.bytes, 0) / 1024),
			decodeMs: median(this.decodes),
			inputToFrameMs: median(this.latencies),
			frames: this.frames,
		};
	}

	/** One line for the HUD. */
	line(now: number): string {
		const s = this.snapshot(now);
		const ms = (value: number | null) => (value === null ? "–" : `${Math.round(value)}ms`);
		return `first ${ms(s.firstFrameMs)} · ${s.fps}fps · ${s.kbps}KB/s · decode ${ms(s.decodeMs)} · input ${ms(s.inputToFrameMs)}`;
	}

	/** How many samples it holds: bounded however long the stream runs. */
	retained(): number {
		return this.arrivals.length + this.shownAt.length + this.decodes.length + this.latencies.length;
	}
}
