import type { BrowserLiveFrame } from "./browser-live-types";

type DebuggerLike = {
	on(event: "message", listener: (event: unknown, method: string, params: Record<string, unknown>) => void): unknown;
	off?(event: "message", listener: (event: unknown, method: string, params: Record<string, unknown>) => void): unknown;
	removeListener?(event: "message", listener: (event: unknown, method: string, params: Record<string, unknown>) => void): unknown;
	sendCommand(method: string, params?: Record<string, unknown>): Promise<unknown>;
};

export class BrowserScreencast {
	private sequence = 0n;
	private stopped = false;
	private lastFrameAt = 0;
	private fallbackTimer?: ReturnType<typeof setInterval>;
	private fallbackPending = false;
	private fallbackDelay = 500;
	private lastJPEG?: Buffer;
	private inputGeneration = 0;
	private readonly listener: (event: unknown, method: string, params: Record<string, unknown>) => void;

	constructor(
		private readonly debug: DebuggerLike,
		private readonly emit: (frame: BrowserLiveFrame) => void,
		private readonly fail: (code: string, message: string) => void,
		private readonly viewport: () => { width: number; height: number } = () => ({ width: 1280, height: 720 }),
		private readonly needsHiddenPaint: () => boolean = () => true,
	) {
		this.listener = (_event, method, params) => {
			if (method !== "Page.screencastFrame" || this.stopped) return;
			void this.consume(params);
		};
	}

	async start(): Promise<void> {
		this.debug.on("message", this.listener);
		try {
			await this.debug.sendCommand("Page.enable");
			await this.debug.sendCommand("Page.startScreencast", {
				format: "jpeg",
				quality: 70,
				maxWidth: 1280,
				maxHeight: 720,
				everyNthFrame: 1,
			});
			if (!this.stopped) {
				this.lastFrameAt = Date.now();
				this.fallbackTimer = setInterval(() => void this.captureHiddenFrame(), 500);
				this.fallbackTimer.unref?.();
			}
		} catch (error) {
			this.removeListener();
			throw error;
		}
	}

	async stop(): Promise<void> {
		if (this.stopped) return;
		this.stopped = true;
		if (this.fallbackTimer) clearInterval(this.fallbackTimer);
		this.fallbackTimer = undefined;
		this.removeListener();
		try {
			await this.debug.sendCommand("Page.stopScreencast");
		} catch {
			// The tab/debugger may already be gone; local teardown still wins.
		}
	}

	// Input wakes a backed-off hidden page on the next capture tick.
	requestFrame(): void {
		this.inputGeneration += 1;
		this.fallbackDelay = 500;
		this.lastFrameAt = Date.now() - 500;
	}

	private removeListener() {
		if (this.debug.off) this.debug.off("message", this.listener);
		else this.debug.removeListener?.("message", this.listener);
	}

	// Chromium stops screencast events when a native surface is hidden or its
	// window is minimized. Request a clipped paint without unhiding the view,
	// with at most one screenshot in flight.
	private async captureHiddenFrame(): Promise<void> {
		// Visible surfaces already publish on repaint. Capturing a scaled clip
		// there can disturb Chromium's on-screen compositor after idle periods.
		if (this.stopped || !this.needsHiddenPaint() || this.fallbackPending || Date.now() - this.lastFrameAt < this.fallbackDelay) return;
		this.fallbackPending = true;
		const inputGeneration = this.inputGeneration;
		try {
			const { width, height } = this.viewport();
			const scale = Math.min(1, 1280 / width, 720 / height);
			const metrics = await this.debug.sendCommand("Page.getLayoutMetrics") as {
				cssVisualViewport?: { pageX: number; pageY: number };
			};
			if (this.stopped || !this.needsHiddenPaint()) return;
			const x = metrics?.cssVisualViewport?.pageX ?? 0;
			const y = metrics?.cssVisualViewport?.pageY ?? 0;
			const result = await this.debug.sendCommand("Page.captureScreenshot", {
				format: "jpeg", quality: 70, fromSurface: true,
				captureBeyondViewport: true,
				clip: { x, y, width, height, scale },
			}) as { data?: string };
			if (!this.stopped && typeof result?.data === "string") {
				const changed = this.emitJPEG(result.data);
				if (inputGeneration === this.inputGeneration) {
					this.fallbackDelay = changed ? 500 : Math.min(5000, this.fallbackDelay * 2);
					this.lastFrameAt = Date.now();
				}
			}
		} catch {
			// Navigation or teardown can temporarily remove the capture target.
			// Retry on the next tick; the stream owner reports target closure.
		} finally {
			this.fallbackPending = false;
		}
	}

	private emitJPEG(encoded: string): boolean {
		if (this.stopped) return false;
		const jpeg = Buffer.from(encoded, "base64");
		if (jpeg.byteLength === 0 || jpeg.byteLength > 2 << 20) throw new Error("Browser frame is empty or too large");
		if (this.lastJPEG?.equals(jpeg)) return false;
		const dimensions = jpegDimensions(jpeg);
		this.lastJPEG = jpeg;
		this.fallbackDelay = 500;
		this.sequence += 1n;
		this.lastFrameAt = Date.now();
		this.emit({
			sequence: this.sequence,
			capturedAtMs: BigInt(Date.now()),
			width: dimensions.width,
			height: dimensions.height,
			jpeg,
		});
		return true;
	}

	private async consume(params: Record<string, unknown>): Promise<void> {
		const sessionId = typeof params.sessionId === "number" ? params.sessionId : undefined;
		if (sessionId === undefined) return;
		try {
			// Only a changed frame resets backoff. Screenshot-triggered repaints
			// can arrive after the screenshot promise settles with identical data.
			this.emitJPEG(typeof params.data === "string" ? params.data : "");
		} catch (error) {
			this.fail("BROWSER_CAPTURE_INVALID_FRAME", error instanceof Error ? error.message : "Browser frame was invalid");
		} finally {
			try {
				await this.debug.sendCommand("Page.screencastFrameAck", { sessionId });
			} catch {
				// A detached target will be handled by the owning browser lifecycle.
			}
		}
	}
}

export function jpegDimensions(data: Buffer): { width: number; height: number } {
	if (data.byteLength < 4 || data[0] !== 0xff || data[1] !== 0xd8) throw new Error("Browser frame is not JPEG");
	let offset = 2;
	while (offset + 8 < data.byteLength) {
		if (data[offset] !== 0xff) { offset += 1; continue; }
		const marker = data[offset + 1];
		offset += 2;
		if (marker === 0xd8 || marker === 0xd9) continue;
		if (offset + 2 > data.byteLength) break;
		const length = data.readUInt16BE(offset);
		if (length < 2 || offset + length > data.byteLength) break;
		if ([0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf].includes(marker)) {
			const height = data.readUInt16BE(offset + 3);
			const width = data.readUInt16BE(offset + 5);
			if (width < 1 || height < 1) break;
			return { width, height };
		}
		offset += length;
	}
	throw new Error("Browser JPEG dimensions are unavailable");
}
