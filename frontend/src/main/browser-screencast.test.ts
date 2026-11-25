import { describe, expect, it, vi } from "vitest";
import { BrowserScreencast, jpegDimensions } from "./browser-screencast";

function jpeg(width = 640, height = 360): Buffer {
	const data = Buffer.alloc(21);
	data.set([0xff, 0xd8, 0xff, 0xc0, 0x00, 0x11, 0x08]);
	data.writeUInt16BE(height, 7);
	data.writeUInt16BE(width, 9);
	return data;
}

describe("BrowserScreencast", () => {
	it("reads JPEG dimensions without decoding the image", () => {
		expect(jpegDimensions(jpeg(1280, 720))).toEqual({ width: 1280, height: 720 });
	});

	it("starts bounded JPEG capture and acknowledges frames", async () => {
		let listener: ((event: unknown, method: string, params: Record<string, unknown>) => void) | undefined;
		const sendCommand = vi.fn(async () => undefined);
		const emit = vi.fn();
		const debug = {
			on: (_event: "message", next: typeof listener) => { listener = next; },
			off: vi.fn(),
			sendCommand,
		};
		const cast = new BrowserScreencast(debug, emit, vi.fn());
		await cast.start();
		expect(sendCommand).toHaveBeenCalledWith("Page.startScreencast", expect.objectContaining({ format: "jpeg", maxWidth: 1280, maxHeight: 720 }));
		listener?.({}, "Page.screencastFrame", { sessionId: 9, data: jpeg().toString("base64") });
		await vi.waitFor(() => expect(emit).toHaveBeenCalledWith(expect.objectContaining({ width: 640, height: 360 })));
		expect(sendCommand).toHaveBeenCalledWith("Page.screencastFrameAck", { sessionId: 9 });
		await cast.stop();
		expect(sendCommand).toHaveBeenCalledWith("Page.stopScreencast");
	});
	it("captures bounded frames when the native surface stops painting and stops on teardown", async () => {
		vi.useFakeTimers();
		try {
			const emit = vi.fn();
			let finishCapture: ((value: { data: string }) => void) | undefined;
			const sendCommand = vi.fn(async (method: string) => {
				if (method === "Page.getLayoutMetrics") return { cssVisualViewport: { pageX: 0, pageY: 400 } };
				if (method === "Page.captureScreenshot") return new Promise<{ data: string }>((resolve) => { finishCapture = resolve; });
				return undefined;
			});
			const cast = new BrowserScreencast({ on: vi.fn(), off: vi.fn(), sendCommand }, emit, vi.fn(), () => ({ width: 1920, height: 1080 }));
			await cast.start();
			await vi.advanceTimersByTimeAsync(1500);
			expect(sendCommand.mock.calls.filter(([method]) => method === "Page.captureScreenshot")).toHaveLength(1);
			expect(sendCommand).toHaveBeenCalledWith("Page.captureScreenshot", expect.objectContaining({
				captureBeyondViewport: true, clip: { x: 0, y: 400, width: 1920, height: 1080, scale: 2 / 3 },
			}));
			finishCapture?.({ data: jpeg(1280, 720).toString("base64") });
			await vi.advanceTimersByTimeAsync(0);
			expect(emit).toHaveBeenCalledWith(expect.objectContaining({ width: 1280, height: 720 }));
			await vi.advanceTimersByTimeAsync(500);
			await cast.stop();
			finishCapture?.({ data: jpeg().toString("base64") });
			await vi.advanceTimersByTimeAsync(1500);
			expect(emit).toHaveBeenCalledTimes(1);
			expect(sendCommand.mock.calls.filter(([method]) => method === "Page.captureScreenshot")).toHaveLength(2);
		} finally { vi.useRealTimers(); }
	});

	it("suppresses duplicate images, backs off to five seconds, and wakes on input", async () => {
		vi.useFakeTimers();
		try {
			const emit = vi.fn();
			let listener: ((event: unknown, method: string, params: Record<string, unknown>) => void) | undefined;
			let image = jpeg().toString("base64");
			const captureTimes: number[] = [];
			const sendCommand = vi.fn(async (method: string) => {
				if (method === "Page.captureScreenshot") { captureTimes.push(Date.now()); return { data: image }; }
				return undefined;
			});
			const cast = new BrowserScreencast({ on: (_event, next) => { listener = next; }, off: vi.fn(), sendCommand }, emit, vi.fn());
			await cast.start();
			await vi.advanceTimersByTimeAsync(13000);
			expect(emit).toHaveBeenCalledTimes(1);
			expect(captureTimes.slice(1).map((time, i) => time - captureTimes[i])).toEqual([500, 1000, 2000, 4000, 5000]);
			listener?.({}, "Page.screencastFrame", { sessionId: 7, data: image });
			await vi.advanceTimersByTimeAsync(1000);
			expect(captureTimes).toHaveLength(6);
			image = jpeg(800, 600).toString("base64");
			cast.requestFrame();
			await vi.advanceTimersByTimeAsync(500);
			expect(emit).toHaveBeenCalledTimes(2);
			expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ sequence: 2n, width: 800 }));
			await cast.stop();
		} finally { vi.useRealTimers(); }
	});

	it("acknowledges duplicate screencast frames without retransmitting them", async () => {
		let listener: ((event: unknown, method: string, params: Record<string, unknown>) => void) | undefined;
		const sendCommand = vi.fn(async () => undefined);
		const emit = vi.fn();
		const cast = new BrowserScreencast({ on: (_event, next) => { listener = next; }, off: vi.fn(), sendCommand }, emit, vi.fn());
		await cast.start();
		for (const sessionId of [1, 2]) listener?.({}, "Page.screencastFrame", { sessionId, data: jpeg().toString("base64") });
		expect(emit).toHaveBeenCalledTimes(1);
		expect(sendCommand).toHaveBeenCalledWith("Page.screencastFrameAck", { sessionId: 1 });
		expect(sendCommand).toHaveBeenCalledWith("Page.screencastFrameAck", { sessionId: 2 });
		await cast.stop();
	});

	it("cancels a pending hidden capture when the desktop becomes visible", async () => {
		vi.useFakeTimers();
		try {
			let hidden = true;
			let finishMetrics: ((value: unknown) => void) | undefined;
			const sendCommand = vi.fn(async (method: string) => method === "Page.getLayoutMetrics"
				? new Promise((resolve) => { finishMetrics = resolve; }) : undefined);
			const cast = new BrowserScreencast({ on: vi.fn(), off: vi.fn(), sendCommand }, vi.fn(), vi.fn(), () => ({ width: 800, height: 600 }), () => hidden);
			await cast.start();
			await vi.advanceTimersByTimeAsync(500);
			hidden = false;
			finishMetrics?.({});
			await vi.advanceTimersByTimeAsync(1000);
			expect(sendCommand).not.toHaveBeenCalledWith("Page.captureScreenshot", expect.anything());
			await cast.stop();
		} finally { vi.useRealTimers(); }
	});

	it("leaves visible static pages untouched and resumes fallback only while hidden", async () => {
		vi.useFakeTimers();
		try {
			let hidden = false;
			const sendCommand = vi.fn(async () => undefined);
			const cast = new BrowserScreencast({ on: vi.fn(), off: vi.fn(), sendCommand }, vi.fn(), vi.fn(), () => ({ width: 800, height: 600 }), () => hidden);
			await cast.start();
			await vi.advanceTimersByTimeAsync(30000);
			expect(sendCommand).not.toHaveBeenCalledWith("Page.getLayoutMetrics");
			expect(sendCommand).not.toHaveBeenCalledWith("Page.captureScreenshot", expect.anything());
			hidden = true;
			await vi.advanceTimersByTimeAsync(500);
			expect(sendCommand).toHaveBeenCalledWith("Page.captureScreenshot", expect.objectContaining({ captureBeyondViewport: true }));
			sendCommand.mockClear();
			hidden = false;
			await vi.advanceTimersByTimeAsync(30000);
			expect(sendCommand).not.toHaveBeenCalledWith("Page.captureScreenshot", expect.anything());
			await cast.stop();
		} finally { vi.useRealTimers(); }
	});


	describe("pacing to the phone", () => {
		// A screencast whose frames carry distinct JPEGs (unchanged frames are not sent).
		const setup = () => {
			let listener: ((event: unknown, method: string, params: Record<string, unknown>) => void) | undefined;
			const sendCommand = vi.fn(async (_method: string, _params?: Record<string, unknown>) => undefined);
			const emit = vi.fn();
			const cast = new BrowserScreencast({ on: (_e: "message", next: typeof listener) => { listener = next; }, off: vi.fn(), sendCommand }, emit, vi.fn());
			let n = 0;
			const frame = (sessionId: number) => listener?.({}, "Page.screencastFrame", { sessionId, data: jpeg(640 + ++n, 360).toString("base64") });
			const acks = () => sendCommand.mock.calls.filter(([method]) => method === "Page.screencastFrameAck").map(([, params]) => params?.sessionId);
			return { cast, frame, acks, emit, sendCommand };
		};

		it("acks a frame only once the daemon has written it to the phone", async () => {
			vi.useFakeTimers();
			try {
				const { cast, frame, acks } = setup();
				await cast.start();
				// The daemon has reported before: backpressure is on.
				cast.frameWritten(0n);
				frame(1);
				await vi.advanceTimersByTimeAsync(0);
				expect(acks()).toEqual([]);
				cast.frameWritten(1n);
				await vi.advanceTimersByTimeAsync(0);
				expect(acks()).toEqual([1]);
				await cast.stop();
			} finally {
				vi.useRealTimers();
			}
		});

		it("never stalls: a frame the daemon never reports is acked after a second", async () => {
			vi.useFakeTimers();
			try {
				const { cast, frame, acks } = setup();
				await cast.start();
				cast.frameWritten(0n);
				frame(1);
				await vi.advanceTimersByTimeAsync(999);
				expect(acks()).toEqual([]);
				await vi.advanceTimersByTimeAsync(1);
				expect(acks()).toEqual([1]);
				await cast.stop();
			} finally {
				vi.useRealTimers();
			}
		});

		it("acks at once until the daemon first reports, so an older daemon is not slowed", async () => {
			const { cast, frame, acks } = setup();
			await cast.start();
			frame(1);
			await vi.waitFor(() => expect(acks()).toEqual([1]));
			await cast.stop();
		});

		it("restarts capture at the quality the phone asks for, within bounds", async () => {
			const { cast, sendCommand } = setup();
			await cast.start();
			await cast.setQuality({ quality: 50, maxFps: 15 });
			expect(sendCommand).toHaveBeenLastCalledWith("Page.startScreencast", expect.objectContaining({ quality: 50 }));
			await cast.setQuality({ quality: 5, maxFps: 15 });
			expect(sendCommand).toHaveBeenLastCalledWith("Page.startScreencast", expect.objectContaining({ quality: 40 }));
			await cast.stop();
		});

		it("holds each ack to the frame rate the phone asked for", async () => {
			vi.useFakeTimers();
			try {
				const { cast, frame, acks } = setup();
				await cast.start();
				await cast.setQuality({ quality: 60, maxFps: 10 });
				frame(1);
				await vi.advanceTimersByTimeAsync(0);
				expect(acks()).toEqual([]);
				await vi.advanceTimersByTimeAsync(100);
				expect(acks()).toEqual([1]);
				await cast.stop();
			} finally {
				vi.useRealTimers();
			}
		});

		it("spaces acks to the frame cap even with two frames in flight", async () => {
			vi.useFakeTimers();
			try {
				const { cast, frame, acks } = setup();
				await cast.start();
				await cast.setQuality({ quality: 60, maxFps: 10 });
				// Chromium keeps two screencast frames in flight.
				frame(1);
				frame(2);
				await vi.advanceTimersByTimeAsync(0);
				expect(acks()).toEqual([]);
				await vi.advanceTimersByTimeAsync(100);
				expect(acks()).toEqual([1]);
				await vi.advanceTimersByTimeAsync(99);
				expect(acks()).toEqual([1]);
				await vi.advanceTimersByTimeAsync(1);
				expect(acks()).toEqual([1, 2]);
				await cast.stop();
			} finally {
				vi.useRealTimers();
			}
		});

		it("acks frames in flight in order, each once the phone has it", async () => {
			vi.useFakeTimers();
			try {
				const { cast, frame, acks } = setup();
				await cast.start();
				cast.frameWritten(0n);
				frame(1);
				frame(2);
				await vi.advanceTimersByTimeAsync(0);
				expect(acks()).toEqual([]);
				cast.frameWritten(1n);
				await vi.advanceTimersByTimeAsync(0);
				expect(acks()).toEqual([1]);
				cast.frameWritten(2n);
				await vi.advanceTimersByTimeAsync(0);
				expect(acks()).toEqual([1, 2]);
				await cast.stop();
			} finally {
				vi.useRealTimers();
			}
		});

		it("acks an unchanged frame at once: nothing goes to the phone", async () => {
			vi.useFakeTimers();
			try {
				let listener: ((event: unknown, method: string, params: Record<string, unknown>) => void) | undefined;
				const sendCommand = vi.fn(async (_method: string, _params?: Record<string, unknown>) => undefined);
				const cast = new BrowserScreencast({ on: (_e: "message", next: typeof listener) => { listener = next; }, off: vi.fn(), sendCommand }, vi.fn(), vi.fn());
				await cast.start();
				cast.frameWritten(0n);
				const same = jpeg().toString("base64");
				listener?.({}, "Page.screencastFrame", { sessionId: 1, data: same });
				cast.frameWritten(1n);
				await vi.advanceTimersByTimeAsync(0);
				listener?.({}, "Page.screencastFrame", { sessionId: 2, data: same });
				await vi.advanceTimersByTimeAsync(0);
				const acked = sendCommand.mock.calls.filter(([method]) => method === "Page.screencastFrameAck").map(([, params]) => params?.sessionId);
				expect(acked).toEqual([1, 2]);
				await cast.stop();
			} finally {
				vi.useRealTimers();
			}
		});
	});
});

