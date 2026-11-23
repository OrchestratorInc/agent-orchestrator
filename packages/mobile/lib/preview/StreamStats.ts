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
// Enough for a second at 60 fps, and for the medians below.
const MAX_FRAMES = 96;
const MAX_SAMPLES = 32;

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
	private arrivals: Array<{ at: number; bytes: number }> = [];
	private shownAt: number[] = [];
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
		push(this.arrivals, { at: now, bytes }, MAX_FRAMES);
	}

	/** A frame shown at `now`, decodeMs after it arrived. */
	shown(now: number, decodeMs: number): void {
		this.frames++;
		if (this.firstFrameMs === null && this.connectedAt !== null) this.firstFrameMs = now - this.connectedAt;
		push(this.shownAt, now, MAX_FRAMES);
		push(this.decodes, decodeMs, MAX_SAMPLES);
		if (this.pendingInput !== null) {
			push(this.latencies, now - this.pendingInput, MAX_SAMPLES);
			this.pendingInput = null;
		}
	}

	snapshot(now: number): StreamStatsSnapshot {
		const since = now - WINDOW_MS;
		return {
			firstFrameMs: this.firstFrameMs,
			fps: this.shownAt.filter((at) => at >= since).length,
			kbps: Math.round(this.arrivals.reduce((sum, f) => (f.at >= since ? sum + f.bytes : sum), 0) / 1024),
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
